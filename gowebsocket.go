package gowebsocket

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	logging "github.com/sacOO7/go-logger"
)

type Empty struct {
}

var logger = logging.GetLogger(reflect.TypeOf(Empty{}).PkgPath()).SetLevel(logging.OFF)

func (socket Socket) EnableLogging() {
	logger.SetLevel(logging.TRACE)
}

func (socket Socket) GetLogger() logging.Logger {
	return logger
}

type Socket struct {
	Conn              *websocket.Conn
	WebsocketDialer   *websocket.Dialer
	Url               string
	ConnectionOptions ConnectionOptions
	RequestHeader     http.Header
	OnConnected       func(socket Socket)
	OnTextMessage     func(message string, socket Socket)
	OnBinaryMessage   func(data []byte, socket Socket)
	OnConnectError    func(err error, socket Socket)
	OnDisconnected    func(err error, socket Socket)
	OnPingReceived    func(data string, socket Socket)
	OnPongReceived    func(data string, socket Socket)
	IsConnected       bool
	connMu            *sync.Mutex
	sendMu            *sync.Mutex
	receiveMu         *sync.Mutex
}

type ConnectionOptions struct {
	UseCompression bool
	UseSSL         bool
	Proxy          func(*http.Request) (*url.URL, error)
	Subprotocols   []string
}

// todo Yet to be done
type ReconnectionOptions struct {
}

func New(url string) Socket {
	return Socket{
		Url:           url,
		RequestHeader: http.Header{},
		ConnectionOptions: ConnectionOptions{
			UseCompression: false,
			UseSSL:         true,
		},
		WebsocketDialer: &websocket.Dialer{},
		connMu:          &sync.Mutex{},
		sendMu:          &sync.Mutex{},
		receiveMu:       &sync.Mutex{},
	}
}

func (socket *Socket) setConnectionOptions() {
	socket.WebsocketDialer.EnableCompression = socket.ConnectionOptions.UseCompression
	socket.WebsocketDialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: socket.ConnectionOptions.UseSSL}
	socket.WebsocketDialer.Proxy = socket.ConnectionOptions.Proxy
	socket.WebsocketDialer.Subprotocols = socket.ConnectionOptions.Subprotocols
}

func (socket *Socket) Connect() {
	var err error
	socket.setConnectionOptions()

	socket.connMu.Lock()
	defer socket.connMu.Unlock()

	socket.Conn, _, err = socket.WebsocketDialer.Dial(socket.Url, socket.RequestHeader)

	if err != nil {
		logger.Error.Println("Error while connecting to server ", err)
		socket.IsConnected = false
		if socket.OnConnectError != nil {
			socket.OnConnectError(err, *socket)
		}
		return
	}

	logger.Info.Println("Connected to server")

	socket.IsConnected = true
	if socket.OnConnected != nil {
		socket.OnConnected(*socket)
	}

	defaultPingHandler := socket.Conn.PingHandler()
	socket.Conn.SetPingHandler(func(appData string) error {
		logger.Trace.Println("Received PING from server")
		if socket.OnPingReceived != nil {
			socket.OnPingReceived(appData, *socket)
		}
		return defaultPingHandler(appData)
	})

	defaultPongHandler := socket.Conn.PongHandler()
	socket.Conn.SetPongHandler(func(appData string) error {
		logger.Trace.Println("Received PONG from server")
		if socket.OnPongReceived != nil {
			socket.OnPongReceived(appData, *socket)
		}
		return defaultPongHandler(appData)
	})

	defaultCloseHandler := socket.Conn.CloseHandler()
	socket.Conn.SetCloseHandler(func(code int, text string) error {
		result := defaultCloseHandler(code, text)
		logger.Warning.Println("Disconnected from server ", result)
		socket.IsConnected = false
		if socket.OnDisconnected != nil {
			socket.OnDisconnected(errors.New(text), *socket)
		}
		return result
	})

	go func() {
		for {
			socket.connMu.Lock()
			conn := socket.Conn
			socket.connMu.Unlock()

			if conn == nil {
				time.Sleep(time.Second)
				continue
			}

			socket.receiveMu.Lock()
			messageType, message, err := conn.ReadMessage()
			socket.receiveMu.Unlock()
			if err != nil {
				logger.Error.Println("read:", err)
				socket.setDisconnected(err)
				return
			}
			logger.Info.Println("recv: %s", message)

			switch messageType {
			case websocket.TextMessage:
				if socket.OnTextMessage != nil {
					socket.OnTextMessage(string(message), *socket)
				}
			case websocket.BinaryMessage:
				if socket.OnBinaryMessage != nil {
					socket.OnBinaryMessage(message, *socket)
				}
			}
		}
	}()
}

func (socket *Socket) setDisconnected(err error) {
	socket.IsConnected = false
	if socket.OnDisconnected != nil {
		socket.OnDisconnected(err, *socket)
	}
}

func (socket *Socket) SendPing() {
	err := socket.send(websocket.PingMessage, []byte(""))
	if err != nil {
		logger.Error.Println("write:", err)
		socket.setDisconnected(err)
		return
	}
}

func (socket *Socket) SendText(message string) {
	err := socket.send(websocket.TextMessage, []byte(message))
	if err != nil {
		logger.Error.Println("write:", err)
		socket.setDisconnected(err)
		return
	}
}

func (socket *Socket) SendBinary(data []byte) {
	err := socket.send(websocket.BinaryMessage, data)
	if err != nil {
		logger.Error.Println("write:", err)
		socket.setDisconnected(err)
		return
	}
}

func (socket *Socket) send(messageType int, data []byte) error {
	socket.connMu.Lock()
	conn := socket.Conn
	socket.connMu.Unlock()

	if conn != nil {
		socket.sendMu.Lock()
		err := socket.Conn.WriteMessage(messageType, data)
		socket.sendMu.Unlock()
		return err
	}

	return fmt.Errorf("could not get connection object")
}

func (socket *Socket) Close() {
	err := socket.send(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	if err != nil {
		logger.Error.Println("write close:", err)
	}
	socket.Conn.Close()
	socket.IsConnected = false
	if socket.OnDisconnected != nil {
		socket.OnDisconnected(err, *socket)
	}
}
