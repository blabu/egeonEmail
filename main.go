package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"time"

	nats "github.com/nats-io/nats.go"
	"gopkg.in/yaml.v2"

	"github.com/blabu/email/conf"
	"github.com/blabu/email/dto"
	"github.com/blabu/email/email"
)

type Connection struct {
	isClose  bool
	cred     conf.Queue
	conf     *tls.Config
	con      *nats.Conn
	handlers map[string]nats.MsgHandler
}

func CreateConnection(cred conf.Queue, certPath, keyPath string, timeout time.Duration, attempt int) (Connection, error) {
	var queue = Connection{
		cred:     cred,
		handlers: make(map[string]nats.MsgHandler),
	}
	var err error
	if len(certPath) > 0 && len(keyPath) > 0 {
		if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
			queue.conf = &tls.Config{
				ServerName:         cred.Host,
				Certificates:       []tls.Certificate{cert},
				InsecureSkipVerify: true,
			}
		}
	}
	queue.con, err = queue.connect(timeout, attempt)
	return queue, err
}

func (c *Connection) Subscribe(subject, workersName string, handler nats.MsgHandler) error {
	c.handlers[subject] = handler
	_, err := c.con.QueueSubscribe(subject, workersName, func(msg *nats.Msg) {
		handler(msg)
	})
	return err
}

func (c *Connection) Publish(subject string, data []byte) error {
	return c.con.Publish(subject, data)
}

func (c *Connection) connect(timeout time.Duration, attempt int) (*nats.Conn, error) {
	if c.conf != nil {
		if con, err := nats.Connect(
			fmt.Sprintf("nats://%s", c.cred.Host),
			nats.Secure(c.conf),
			nats.RetryOnFailedConnect(true),
			nats.MaxReconnects(attempt),
			nats.ReconnectWait(timeout),
			nats.UserInfo(c.cred.Login, c.cred.Pass),
			nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
				os.Stderr.WriteString("Client disconnected: " + err.Error())
			}),
			nats.ReconnectHandler(func(_ *nats.Conn) {
				os.Stderr.WriteString("client Reconnected")
			}),
		); err == nil {
			return con, nil
		}
	}
	return nats.Connect(
		fmt.Sprintf("nats://%s", c.cred.Host),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(attempt),
		nats.ReconnectWait(timeout),
		nats.UserInfo(c.cred.Login, c.cred.Pass),
	)
}

func (c *Connection) Close() {
	c.isClose = true
	c.con.Flush()
	c.con.Close()
}

func ServeEmailSender(ch <-chan *dto.Message, poolSz uint16, accaunt *conf.ServerSMTP) func() {
	host, _, _ := net.SplitHostPort(accaunt.Host)
	p, err := email.NewPool(
		accaunt.Host,
		int(poolSz),
		smtp.PlainAuth("", accaunt.Source, accaunt.Pass, host),
	)
	if err != nil {
		return nil
	}
	return func() {
		for msg := range ch {
			var e = email.Email{
				From: msg.From,
				To:   msg.To,
				Cc:   msg.Copy,
			}
			p.Send(&e, time.Duration(accaunt.Timeout)*time.Second)
		}
	}
}

func main() {
	if len(os.Args) != 2 {
		os.Stderr.WriteString("Please insert config file path when run application next time")
		os.Exit(254)
	}
	err := conf.ReadConfig(os.Args[2])
	if err != nil {
		os.Stderr.WriteString(err.Error())
		os.Stderr.WriteString("Please configure application and use it by path " + os.Args[2])
		if f, err := os.Create(os.Args[2]); err == nil {
			yaml.NewDecoder(f).Decode(conf.Config)
		} else {
			os.Stderr.WriteString(err.Error())
		}
		os.Exit(255)
	}
	con, err := CreateConnection(conf.Config.Q, conf.Config.CertPath, conf.Config.KeyPath, time.Duration(conf.Config.ReadTimeout), 10)
	if err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(253)
	}
	messages := make(chan *dto.Message, len(conf.Config.SMTP))
	con.Subscribe(conf.Config.ChannelEmail, conf.Config.WorkersName, func(msg *nats.Msg) {
		var message dto.Message
		err := json.Unmarshal(msg.Data, &message)
		if err != nil {
			msg.Respond([]byte(err.Error()))
		} else {
			messages <- &message
		}
	})
	for _, accaunt := range conf.Config.SMTP {
		if handler := ServeEmailSender(messages, accaunt.Count, &accaunt); handler != nil {
			go handler()
		}
	}
}

/*

func GetHTTPServe(gateway *http.Server, certPath, privateKeyPath string) func() error {
	if len(certPath) > 0 && len(privateKeyPath) > 0 {
		os.Stdout.WriteString("Try start https service with certificat in " + certPath)
		if cert, err := tls.LoadX509KeyPair(certPath, privateKeyPath); err == nil {
			gateway.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
			os.Stdout.WriteString("Start https service")
			return func() error { return gateway.ListenAndServeTLS("", "") }
		}
	}
	os.Stdout.WriteString("Start http service. It's not secure")
	return func() error { return gateway.ListenAndServe() }
}

	router := mux.NewRouter()
	router.Use(mux.CORSMethodMiddleware(router))

	gateway := http.Server{
		Handler:      router,
		Addr:         conf.Config.IP,
		WriteTimeout: time.Duration(conf.Config.ReadTimeout) * time.Second,
		ReadTimeout:  time.Duration(conf.Config.ReadTimeout) * time.Second,
	}

	serve := GetHTTPServe(&gateway, conf.Config.CertPath, conf.Config.KeyPath)
	os.Stderr.WriteString(serve().Error())

*/
