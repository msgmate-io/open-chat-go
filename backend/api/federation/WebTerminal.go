package federation

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"
)

// WebTerminalHandler handles WebSocket connections for the web terminal
func (h *FederationHandler) WebTerminalHandler(w http.ResponseWriter, r *http.Request) {
	// Get SSH server details from query parameters
	portStr := r.URL.Query().Get("port")
	password := r.URL.Query().Get("password")

	if portStr == "" || password == "" {
		http.Error(w, "Missing port or password", http.StatusBadRequest)
		return
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		http.Error(w, "Invalid port number", http.StatusBadRequest)
		return
	}

	// Upgrade HTTP connection to WebSocket using coder/websocket
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection to WebSocket: %v", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "Session ended")

	// Connect to SSH server
	sshConfig := &ssh.ClientConfig{
		User: "tim", // Using a default user, you might want to make this configurable
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	sshClient, err := ssh.Dial("tcp", fmt.Sprintf("localhost:%d", port), sshConfig)
	if err != nil {
		log.Printf("Failed to dial SSH server: %v", err)
		conn.Write(r.Context(), websocket.MessageText, []byte(fmt.Sprintf("Error connecting to SSH server: %v", err)))
		return
	}
	defer sshClient.Close()

	// Create SSH session
	session, err := sshClient.NewSession()
	if err != nil {
		log.Printf("Failed to create SSH session: %v", err)
		conn.Write(r.Context(), websocket.MessageText, []byte(fmt.Sprintf("Error creating SSH session: %v", err)))
		return
	}
	defer session.Close()

	// Request pseudo-terminal
	termWidth, termHeight := 80, 24
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	if err := session.RequestPty("xterm", termHeight, termWidth, modes); err != nil {
		log.Printf("Failed to request PTY: %v", err)
		conn.Write(r.Context(), websocket.MessageText, []byte(fmt.Sprintf("Error requesting PTY: %v", err)))
		return
	}

	// Create pipes for stdin, stdout, stderr
	stdin, err := session.StdinPipe()
	if err != nil {
		log.Printf("Failed to get stdin pipe: %v", err)
		return
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		log.Printf("Failed to get stdout pipe: %v", err)
		return
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		log.Printf("Failed to get stderr pipe: %v", err)
		return
	}

	// Start remote shell
	if err := session.Shell(); err != nil {
		log.Printf("Failed to start shell: %v", err)
		conn.Write(r.Context(), websocket.MessageText, []byte(fmt.Sprintf("Error starting shell: %v", err)))
		return
	}

	ctx := r.Context()

	// Handle WebSocket messages (terminal input)
	go func() {
		for {
			messageType, message, err := conn.Read(ctx)
			if err != nil {
				if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
					log.Printf("WebSocket read error: %v", err)
				}
				return
			}

			if messageType == websocket.MessageText {
				// Try to parse as JSON for commands
				var cmd struct {
					Type string `json:"type"`
					Data string `json:"data"`
					Cols int    `json:"cols,omitempty"`
					Rows int    `json:"rows,omitempty"`
				}

				if err := json.Unmarshal(message, &cmd); err == nil {
					switch cmd.Type {
					case "input":
						// Send input to SSH session
						_, err = stdin.Write([]byte(cmd.Data))
						if err != nil {
							log.Printf("Error writing to stdin: %v", err)
							return
						}
					case "resize":
						// Resize terminal
						err = session.WindowChange(cmd.Rows, cmd.Cols)
						if err != nil {
							log.Printf("Error resizing terminal: %v", err)
						}
					}
				} else {
					// If not JSON, treat as raw input
					_, err = stdin.Write(message)
					if err != nil {
						log.Printf("Error writing to stdin: %v", err)
						return
					}
				}
			} else if messageType == websocket.MessageBinary {
				// Handle binary data (e.g., for special keys)
				_, err = stdin.Write(message)
				if err != nil {
					log.Printf("Error writing binary data to stdin: %v", err)
					return
				}
			}
		}
	}()

	// Forward SSH output to WebSocket
	var wg sync.WaitGroup
	wg.Add(2)

	// Handle stdout
	go func() {
		defer wg.Done()
		buffer := make([]byte, 1024)
		for {
			n, err := stdout.Read(buffer)
			if err != nil {
				if err != io.EOF {
					log.Printf("SSH stdout read error: %v", err)
				}
				return
			}

			err = conn.Write(ctx, websocket.MessageBinary, buffer[:n])
			if err != nil {
				log.Printf("WebSocket write error: %v", err)
				return
			}
		}
	}()

	// Handle stderr
	go func() {
		defer wg.Done()
		buffer := make([]byte, 1024)
		for {
			n, err := stderr.Read(buffer)
			if err != nil {
				if err != io.EOF {
					log.Printf("SSH stderr read error: %v", err)
				}
				return
			}

			err = conn.Write(ctx, websocket.MessageBinary, buffer[:n])
			if err != nil {
				log.Printf("WebSocket write error: %v", err)
				return
			}
		}
	}()

	// Wait for session to end
	wg.Wait()
	session.Wait()
}
