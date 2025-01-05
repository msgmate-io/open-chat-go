package federation

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
)

type SSHServer struct {
	config   *ssh.ServerConfig
	port     int
	password string
	listener net.Listener
	done     chan struct{}
}

func (h *FederationHandler) StartSSHProxy(port int, password string) error {
	// Start SSH server
	server, err := NewSSHServer(port, password)
	if err != nil {
		return fmt.Errorf("failed to create SSH server: %w", err)
	}

	// Start server in goroutine
	go func() {
		err := server.Start()
		if err != nil {
			log.Printf("SSH server error: %v", err)
		}
	}()

	return nil
}

func generateRandomPassword(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes)[:length], nil
}

func NewSSHServer(port int, password string) (*SSHServer, error) {

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %v", err)
	}

	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == password {
				return nil, nil
			}
			return nil, fmt.Errorf("incorrect password")
		},
	}

	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create signer: %v", err)
	}
	config.AddHostKey(signer)

	server := &SSHServer{
		config:   config,
		port:     port,
		password: password,
	}

	log.Printf("SSH server created with password: %s", password)
	return server, nil
}

func (s *SSHServer) Shutdown() {
	if s.listener != nil {
		s.listener.Close()
	}
	close(s.done)
}

func (s *SSHServer) Start() error {
	var err error
	s.listener, err = net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %v", s.port, err)
	}
	defer s.listener.Close()

	s.done = make(chan struct{})
	log.Printf("SSH server listening on port %d", s.port)

	go func() {
		<-s.done
		s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if err != net.ErrClosed {
				log.Printf("Failed to accept connection: %v", err)
			}
			return err
		}
		go s.handleConnection(conn)
	}
}

func (s *SSHServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.config)
	if err != nil {
		log.Printf("Failed to handshake: %v", err)
		return
	}
	defer sshConn.Close()

	log.Printf("New SSH connection from %s (%s)", sshConn.RemoteAddr(), sshConn.ClientVersion())

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			log.Printf("Failed to accept channel: %v", err)
			continue
		}

		go s.handleChannel(channel, requests)
	}
}

func (s *SSHServer) handleChannel(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()

	// Get the default shell
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	// Start the shell in login mode
	cmd := exec.Command(shell, "-l") // The '-l' flag starts the shell as a login shell

	// Use the existing environment
	cmd.Env = os.Environ()
	cmd.Env = append(os.Environ(), "TERM=xterm")

	// Create PTY
	f, err := pty.Start(cmd)
	if err != nil {
		log.Printf("Failed to start pty: %v", err)
		return
	}
	defer f.Close()

	// Handle PTY requests
	go func() {
		for req := range requests {
			switch req.Type {
			case "shell":
				req.Reply(true, nil)
			case "pty-req":
				termLen := req.Payload[3]
				w := uint32(req.Payload[termLen+4])<<24 | uint32(req.Payload[termLen+5])<<16 | uint32(req.Payload[termLen+6])<<8 | uint32(req.Payload[termLen+7])
				h := uint32(req.Payload[termLen+8])<<24 | uint32(req.Payload[termLen+9])<<16 | uint32(req.Payload[termLen+10])<<8 | uint32(req.Payload[termLen+11])
				pty.Setsize(f, &pty.Winsize{
					Rows: uint16(h),
					Cols: uint16(w),
				})
				req.Reply(true, nil)
			case "window-change":
				w := uint32(req.Payload[0])<<24 | uint32(req.Payload[1])<<16 | uint32(req.Payload[2])<<8 | uint32(req.Payload[3])
				h := uint32(req.Payload[4])<<24 | uint32(req.Payload[5])<<16 | uint32(req.Payload[6])<<8 | uint32(req.Payload[7])
				pty.Setsize(f, &pty.Winsize{
					Rows: uint16(h),
					Cols: uint16(w),
				})
			}
		}
	}()

	// Start copying data
	go func() {
		io.Copy(channel, f)
		channel.Close()
	}()
	io.Copy(f, channel)

	cmd.Wait()
}

func SSHSession(host string, port string, username string, password string) {
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	// Connect to the SSH server
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%s", host, port), config)
	if err != nil {
		log.Printf("Failed to dial: %v", err)
		return
	}
	defer client.Close()

	// Create a new session
	session, err := client.NewSession()
	if err != nil {
		log.Printf("Failed to create session: %v", err)
		return
	}
	defer session.Close()

	// Set up standard input, output, and error
	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	// Set terminal into raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		log.Printf("Failed to set terminal to raw mode: %v", err)
		return
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Start an interactive shell
	err = session.Shell()
	if err != nil {
		log.Printf("Failed to start shell: %v", err)
		return
	}

	// Wait for the session to complete
	err = session.Wait()
	if err != nil {
		log.Printf("Session ended with error: %v", err)
	}
}
