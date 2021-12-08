package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"time"

	"github.com/gorilla/mux"
	nats "github.com/nats-io/nats.go"
	"gopkg.in/yaml.v2"

	"github.com/blabu/egeonEmail/conf"
	"github.com/blabu/egeonEmail/dto"
	"github.com/blabu/egeonEmail/email"
)

type Subscriber struct {
	isClose  bool
	log      io.StringWriter
	cred     conf.Queue
	conf     *tls.Config
	con      *nats.Conn
	handlers map[string]nats.MsgHandler
}

func CreateConnection(cred conf.Queue, timeout time.Duration, log io.StringWriter) (*Subscriber, error) {
	var queue = Subscriber{
		cred:     cred,
		handlers: make(map[string]nats.MsgHandler),
		log:      log,
	}
	var err error
	if len(cred.ClientCert) > 0 && len(cred.ClientKey) > 0 {
		if cert, err := tls.LoadX509KeyPair(cred.ClientCert, cred.ClientKey); err == nil {
			queue.conf = &tls.Config{
				MinVersion:         tls.VersionTLS12,
				ServerName:         cred.Host,
				Certificates:       []tls.Certificate{cert},
				InsecureSkipVerify: false,
			}
		}
	}
	if data, err := ioutil.ReadFile(cred.CA); err == nil && data != nil {
		rootPEM := x509.NewCertPool()
		if ok := rootPEM.AppendCertsFromPEM(data); !ok {
			return nil, errors.New("Can not parse a root certificate from file " + cred.CA)
		}
		if queue.conf == nil {
			queue.conf = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		queue.conf.RootCAs = rootPEM
	}
	queue.con, err = queue.connect(timeout, cred.Attempt)
	return &queue, err
}

func (c *Subscriber) Subscribe(subject, workersName string, handler nats.MsgHandler) error {
	c.handlers[subject] = handler
	_, err := c.con.QueueSubscribe(subject, workersName, func(msg *nats.Msg) {
		handler(msg)
	})
	return err
}

func (c *Subscriber) Status() (string, error) {
	if c.con == nil {
		return "", errors.New("Connection is null")
	}
	stat := c.con.Status()
	if stat != nats.CONNECTED {
		return "", errors.New("Connection status: " + stat.String())
	}
	return stat.String(), nil
}

func (c *Subscriber) connect(timeout time.Duration, attempt int) (*nats.Conn, error) {
	con, err := nats.Connect(
		fmt.Sprintf("tls://%s", c.cred.Host),
		nats.Secure(c.conf),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(attempt),
		nats.ReconnectWait(timeout),
		nats.UserInfo(c.cred.Login, c.cred.Pass),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				c.log.WriteString("Client disconnected: " + err.Error() + "\n")
			} else {
				c.log.WriteString("Client disconnected")
			}
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			c.log.WriteString("Client Reconnected\n")
		}),
	)
	return con, err
}

func (c *Subscriber) Close() {
	c.log.WriteString("Close connection")
	c.isClose = true
	c.con.Flush()
	c.con.Close()
}

func ServeEmailSender(ch <-chan *dto.Message, poolSz uint16, accaunt *conf.ServerSMTP, log io.StringWriter) func() {
	host, _, _ := net.SplitHostPort(accaunt.Host)
	p, err := email.NewPool(
		accaunt.Host,
		int(poolSz),
		smtp.PlainAuth("", accaunt.Source, accaunt.Pass, host),
	)
	if err != nil {
		log.WriteString(fmt.Sprintf("Error %s when try create email sender pool\n", err.Error()))
		return nil
	}
	return func() {
		defer p.Close()
		for msg := range ch {
			var e = email.Email{
				From: msg.From,
				To:   msg.To,
				Cc:   msg.Copy,
			}
			if err := p.Send(&e, time.Duration(accaunt.Timeout)*time.Second); err != nil {
				log.WriteString(fmt.Sprintf("Error %s when try send email\n", err.Error()))
			}
		}
	}
}

func GetReceiveMessageHandler(messages chan<- *dto.Message, log io.StringWriter) nats.MsgHandler {
	return func(msg *nats.Msg) {
		var message dto.Message
		if err := json.Unmarshal(msg.Data, &message); err != nil {
			log.WriteString(fmt.Sprintf("Error %s. When try read from channel %s\n", err.Error(), conf.Config.ChannelEmail))
		} else {
			hash := sha256.Sum256([]byte(message.Data))
			message.Hash = base64.StdEncoding.EncodeToString(hash[:])
			message.Timestamp = time.Now().UnixNano()
			messages <- &message
		}
	}
}

func GetEmailSenderHandler(messages chan<- *dto.Message) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var msg dto.Message
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			w.Header().Add("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(err.Error())
			return
		}
		hash := sha256.Sum256([]byte(msg.Data))
		msg.Hash = base64.StdEncoding.EncodeToString(hash[:])
		msg.Timestamp = time.Now().UnixNano()
		messages <- &msg
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(msg)
	}
}

//GetStatusHandler - return status handler that show current status of service and base documentation about service interface
func GetStatusHandler(channelName, workersName string, con *Subscriber) http.HandlerFunc {
	type answer struct {
		Entity  map[string]string `json:"entity"`
		Methods map[string]string `json:"methods"`
	}
	var ans answer
	ans.Entity = make(map[string]string)
	ans.Methods = make(map[string]string)

	startTime := time.Now()
	messageForm, _ := json.Marshal(dto.Message{})
	ans.Entity["message"] = string(messageForm)
	ans.Entity["startTime"] = startTime.Format("2006-01-02 15:04:06")
	ans.Entity["version"] = "v0.0.0"
	ans.Entity["author"] = "Oleksiy Khanin"
	ans.Entity["app"] = os.Args[0]
	ans.Methods["/v1/email POST"] = "Try send an email. dto.Message must be in request body"
	ans.Methods["/status GET"] = "Get this status of service and documentation"
	ans.Methods[fmt.Sprintf("tls://%s/queue/%s/%s", con.cred.Host, channelName, workersName)] = "Put email into message queue. External NATS service"
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := con.Status()
		if err != nil {
			ans.Entity["nats"] = err.Error()
		} else {
			ans.Entity["nats"] = status
		}
		ans.Entity["workTime"] = time.Now().Sub(startTime).String()
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(ans)
	}
}

type notfind struct{}

func (n *notfind) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte("{\"err\": \"Not Found, sorry\"}"))
}

func main() {
	if len(os.Args) != 2 {
		os.Stderr.WriteString("Please insert config file path when run application next time\n")
		os.Exit(254)
	}
	err := conf.ReadConfig(os.Args[1])
	if err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Stderr.WriteString("Please configure application and use it by path " + os.Args[1] + "\n")
		if f, err := os.Create(os.Args[1]); err == nil {
			conf.Config.SMTP = make([]conf.ServerSMTP, 0)
			conf.Config.SMTP = append(conf.Config.SMTP, conf.ServerSMTP{})
			yaml.NewEncoder(f).Encode(conf.Config)
		} else {
			os.Stderr.WriteString(err.Error() + "\n")
		}
		os.Exit(255)
	}
	sub, err := CreateConnection(conf.Config.Q, time.Duration(conf.Config.ReadTimeout), os.Stderr)
	if err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(253)
	}
	defer sub.Close()
	messages := make(chan *dto.Message, len(conf.Config.SMTP))
	sub.Subscribe(conf.Config.ChannelEmail, conf.Config.WorkersName, GetReceiveMessageHandler(messages, os.Stderr))
	for _, accaunt := range conf.Config.SMTP {
		if handler := ServeEmailSender(messages, accaunt.Count, &accaunt, os.Stderr); handler != nil {
			go handler()
		}
	}
	router := mux.NewRouter()
	router.Use(mux.CORSMethodMiddleware(router))
	router.Path("/v1/email").Methods("POST").HandlerFunc(GetEmailSenderHandler(messages))
	router.Path("/status").Methods("GET").HandlerFunc(GetStatusHandler(conf.Config.ChannelEmail, conf.Config.WorkersName, sub))
	router.NotFoundHandler = &notfind{}
	gateway := http.Server{
		Handler:      router,
		Addr:         conf.Config.IP,
		WriteTimeout: time.Duration(conf.Config.ReadTimeout) * time.Second,
		ReadTimeout:  time.Duration(conf.Config.ReadTimeout) * time.Second,
	}

	serve := GetHTTPServe(&gateway, conf.Config.CertPath, conf.Config.KeyPath, os.Stdout)
	os.Stderr.WriteString(serve().Error() + "\n")
}

func GetHTTPServe(gateway *http.Server, certPath, privateKeyPath string, log io.StringWriter) func() error {
	if len(certPath) > 0 && len(privateKeyPath) > 0 {
		log.WriteString("Try start https service with certificat in " + certPath + "\n")
		if cert, err := tls.LoadX509KeyPair(certPath, privateKeyPath); err == nil {
			gateway.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
			log.WriteString("Start https service\n")
			return func() error { return gateway.ListenAndServeTLS("", "") }
		}
	}
	log.WriteString(fmt.Sprintf("Start http service on %s. It's not secure\n", gateway.Addr))
	return func() error { return gateway.ListenAndServe() }
}
