package websocket

// TODO: Also implement a redis-compoatible handler

import (
	"backend/sharecontent"
	"context"
	"github.com/coder/websocket"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

type Subscriber struct {
	msgs     chan []byte
	UserUUID string
	ChatUUID string
	// ShareContentMode is empty for regular (authenticated) subscribers and set
	// to the share's content mode for shared interaction viewers. Shared
	// subscribers only ever receive redacted frames, so sensitive payloads such
	// as tool call parameters, tool outputs and message meta data can not leak
	// through a live broadcast.
	ShareContentMode string
	closeSlow        func()
}

type WebSocketHandler struct {
	subscriberMessageBuffer int
	// publishLimiter *rate.Limiter
	MessageHandler *Messages
	logf           func(f string, v ...interface{})
	serveMux       http.ServeMux
	subscribersMu  sync.Mutex
	subscribers    map[*Subscriber]struct{}
	connectionsMu  sync.Mutex
	connections    map[*websocket.Conn]struct{}
}

func (cs *WebSocketHandler) GetSubscribers() []Subscriber {
	cs.subscribersMu.Lock()
	defer cs.subscribersMu.Unlock()

	subscribers := make([]Subscriber, 0, len(cs.subscribers))
	for s := range cs.subscribers {
		subscribers = append(subscribers, *s)
	}
	return subscribers
}

func NewWebSocketHandler() *WebSocketHandler {
	return &WebSocketHandler{
		subscriberMessageBuffer: 100,
		// publishLimiter: rate.NewLimiter(rate.Limit(1), 1),
		MessageHandler: &Messages{},
		logf: func(f string, v ...interface{}) {
			log.Printf(f, v...)
		},
		subscribers: make(map[*Subscriber]struct{}),
		connections: make(map[*websocket.Conn]struct{}),
	}
}

func (cs *WebSocketHandler) addConnection(conn *websocket.Conn) {
	if conn == nil {
		return
	}
	cs.connectionsMu.Lock()
	cs.connections[conn] = struct{}{}
	cs.connectionsMu.Unlock()
}

func (cs *WebSocketHandler) deleteConnection(conn *websocket.Conn) {
	if conn == nil {
		return
	}
	cs.connectionsMu.Lock()
	delete(cs.connections, conn)
	cs.connectionsMu.Unlock()
}

func (cs *WebSocketHandler) Shutdown() {
	cs.connectionsMu.Lock()
	conns := make([]*websocket.Conn, 0, len(cs.connections))
	for conn := range cs.connections {
		conns = append(conns, conn)
	}
	cs.connectionsMu.Unlock()

	for _, conn := range conns {
		conn.CloseNow()
	}
}

func (cs *WebSocketHandler) PublishInChannel(msg []byte, receiverUUID string) {
	cs.subscribersMu.Lock()
	defer cs.subscribersMu.Unlock()

	for s := range cs.subscribers {
		if s.UserUUID == receiverUUID {
			select {
			case s.msgs <- msg:
			default:
				go s.closeSlow()
			}
		}
	}
}

func (cs *WebSocketHandler) PublishInChatChannel(msg []byte, chatUUID string) {
	cs.subscribersMu.Lock()
	defer cs.subscribersMu.Unlock()

	for s := range cs.subscribers {
		if s.ChatUUID == chatUUID {
			cs.sendToSubscriber(s, msg)
		}
	}
}

// sendToSubscriber queues a frame for a single subscriber, applying the share
// redaction rules for shared interaction viewers. The caller must hold
// subscribersMu.
func (cs *WebSocketHandler) sendToSubscriber(s *Subscriber, msg []byte) {
	payload := msg
	if s.ShareContentMode != "" {
		payload = sharecontent.RedactJSON(msg, sharecontent.Mode(s.ShareContentMode))
	}
	select {
	case s.msgs <- payload:
	default:
		go s.closeSlow()
	}
}

func (cs *WebSocketHandler) Publish(msg []byte) {
	cs.subscribersMu.Lock()
	defer cs.subscribersMu.Unlock()

	// cs.publishLimiter.Wait(context.Background())

	for s := range cs.subscribers {
		select {
		case s.msgs <- msg:
		default:
			go s.closeSlow()
		}
	}
}

func (cs *WebSocketHandler) addSubscriber(s *Subscriber) {
	cs.subscribersMu.Lock()
	cs.subscribers[s] = struct{}{}
	cs.subscribersMu.Unlock()
}

func (cs *WebSocketHandler) deleteSubscriber(s *Subscriber) {
	cs.subscribersMu.Lock()
	delete(cs.subscribers, s)
	cs.subscribersMu.Unlock()
}

func writeTimeout(ctx context.Context, timeout time.Duration, c *websocket.Conn, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return c.Write(ctx, websocket.MessageText, msg)
}

func (cs *WebSocketHandler) SubscribeChannel(w http.ResponseWriter, r *http.Request, userUUID string) error {

	var mu sync.Mutex
	var c *websocket.Conn
	var closed bool
	s := &Subscriber{
		UserUUID: userUUID,
		msgs:     make(chan []byte, cs.subscriberMessageBuffer),
		closeSlow: func() {
			mu.Lock()
			defer mu.Unlock()
			closed = true
			if c != nil {
				c.Close(websocket.StatusPolicyViolation, "connection too slow TBS to keep up with messages")
			}
		},
	}
	cs.addSubscriber(s)
	defer cs.deleteSubscriber(s)

	c2, err := cs.acceptConnection(w, r)
	cs.logf("accept connection")
	if err != nil {
		return err
	}
	mu.Lock()
	if closed {
		mu.Unlock()
		return net.ErrClosed
	}
	c = c2
	mu.Unlock()
	cs.addConnection(c)
	defer cs.deleteConnection(c)
	defer c.CloseNow()

	ctx := c.CloseRead(context.Background())
	cs.logf("new connection")

	for {
		select {
		case msg := <-s.msgs:
			err := writeTimeout(ctx, time.Second*200, c, msg)
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (cs *WebSocketHandler) SubscribeChatChannel(w http.ResponseWriter, r *http.Request, chatUUID string, contentMode sharecontent.Mode) error {
	var mu sync.Mutex
	var c *websocket.Conn
	var closed bool
	s := &Subscriber{
		ChatUUID:         chatUUID,
		ShareContentMode: string(sharecontent.NormalizeMode(string(contentMode))),
		msgs:             make(chan []byte, cs.subscriberMessageBuffer),
		closeSlow: func() {
			mu.Lock()
			defer mu.Unlock()
			closed = true
			if c != nil {
				c.Close(websocket.StatusPolicyViolation, "connection too slow TBS to keep up with messages")
			}
		},
	}
	cs.addSubscriber(s)
	defer cs.deleteSubscriber(s)

	c2, err := cs.acceptConnection(w, r)
	cs.logf("accept shared interaction connection")
	if err != nil {
		return err
	}
	mu.Lock()
	if closed {
		mu.Unlock()
		return net.ErrClosed
	}
	c = c2
	mu.Unlock()
	cs.addConnection(c)
	defer cs.deleteConnection(c)
	defer c.CloseNow()

	ctx := c.CloseRead(context.Background())
	cs.logf("new shared interaction connection for chat %s", chatUUID)

	for {
		select {
		case msg := <-s.msgs:
			err := writeTimeout(ctx, time.Second*200, c, msg)
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
