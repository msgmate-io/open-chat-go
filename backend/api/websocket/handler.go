package websocket

// TODO: Also implement a redis-compoatible handler

import (
	"context"
	"github.com/coder/websocket"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

type Subscriber struct {
	msgs      chan []byte
	UserUUID  string
	closeSlow func()
}

type WebSocketHandler struct {
	subscriberMessageBuffer int
	// publishLimiter *rate.Limiter
	MessageHandler *Messages
	logf           func(f string, v ...interface{})
	serveMux       http.ServeMux
	subscribersMu  sync.Mutex
	subscribers    map[*Subscriber]struct{}
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
		subscriberMessageBuffer: 10,
		// publishLimiter: rate.NewLimiter(rate.Limit(1), 1),
		MessageHandler: &Messages{},
		logf: func(f string, v ...interface{}) {
			log.Printf(f, v...)
		},
		subscribers: make(map[*Subscriber]struct{}),
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
				c.Close(websocket.StatusPolicyViolation, "connection too slow to keep up with messages")
			}
		},
	}
	cs.addSubscriber(s)
	defer cs.deleteSubscriber(s)

	c2, err := websocket.Accept(w, r, nil)
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
	defer c.CloseNow()

	ctx := c.CloseRead(context.Background())
	cs.logf("new connection")

	for {
		select {
		case msg := <-s.msgs:
			err := writeTimeout(ctx, time.Second*5, c, msg)
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
