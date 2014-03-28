package main

import (
	"encoding/json"
	"fmt"
	"github.com/geoloqi/geobin-go/socket"
	"github.com/gorilla/mux"
	redis "github.com/vmihailenco/redis/v2"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

type Config struct {
	Port       int
	RedisHost  string
	RedisPass  string
	RedisDB    int64
	NameVals   string
	NameLength int
}

// todo: determine if these need to be threadsafe (pretty sure they do)
var config = &Config{}
var client = &redis.Client{}
var pubsub = &redis.PubSub{}
var sockets = make(map[string] socket.S)

type GeobinRequest struct {
	Timestamp int64             `json:"timestamp"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"`
}

func init() {
	// add file info to log statements
	log.SetFlags(log.Ldate | log.Ltime | log.Llongfile)
	// set up unique seed for random num generation
	rand.Seed(time.Now().UTC().UnixNano())

	// prepare router
	r := mux.NewRouter()
	r.HandleFunc("/create", create)
	r.HandleFunc("/{name}", existing)
	r.HandleFunc("/history/{name}", history)
	r.HandleFunc("/ws/{name}", openSocket)
	r.PathPrefix("/").Handler(http.FileServer(http.Dir("./static/")))
	http.Handle("/", r)

	// load up the config file
	file, err := os.Open("config.json")
	if err != nil {
		log.Fatal(err)
	}
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		log.Fatal(err)
	}

	// prepare redis
	client = redis.NewTCPClient(&redis.Options{
		Addr:     config.RedisHost,
		Password: config.RedisPass,
		DB:       config.RedisDB,
	})

	if ping := client.Ping(); ping.Err() != nil {
		log.Fatal(ping.Err())
	}
	pubsub = client.PubSub()
}

func main() {
	// loop for receiving messages from Redis pubsub, and forwarding them on to relevant ws connection
	go redisPump()

	defer func() {
		pubsub.Close()
		client.Close()
	}()
	err := http.ListenAndServe(fmt.Sprintf(":%d", config.Port), nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func create(w http.ResponseWriter, r *http.Request) {
	n, err := randomString(config.NameLength)
	if err != nil {
		log.Println("Failure to create new name:", n, err)
		http.Error(w, "Could not generate new Geobin!", http.StatusInternalServerError)
		return
	}

	if res := client.ZAdd(n, redis.Z{0, ""}); res.Err() != nil {
		log.Println("Failure to ZADD to", n, res.Err())
		http.Error(w, "Could not generate new Geobin!", http.StatusInternalServerError)
		return
	}

	if res := client.Expire(n, 48*time.Hour); res.Err() != nil {
		log.Println("Failure to set EXPIRE for", n, res.Err())
		http.Error(w, "Could not generate new Geobin!", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/"+n, http.StatusFound)
}

func existing(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]

	exists, err := nameExists(name)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	if !exists {
		http.NotFound(w, r)
		return
	}

	if r.Method == "POST" {
		defer r.Body.Close()
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			log.Println("Error while reading POST body:", err)
			http.Error(w, "Could not read POST body!", http.StatusInternalServerError)
			return
		}

		headers := make(map[string]string)
		for k, v := range r.Header {
			headers[k] = strings.Join(v, ", ")
		}

		gr := GeobinRequest{
			Timestamp: time.Now().UTC().Unix(),
			Headers:   headers,
			Body:      string(body),
		}

		encoded, err := json.Marshal(gr)
		if err != nil {
			log.Println("Error marshalling request:", err)
		}

		if res := client.ZAdd(name, redis.Z{float64(time.Now().UTC().Unix()), string(encoded)}); res.Err() != nil {
			log.Println("Failure to ZADD to", name, res.Err())
		}

		if res := client.Publish(name, string(encoded)); res.Err() != nil {
			log.Println("Failure to PUBLISH to", name, res.Err())
		}
	} else if r.Method == "GET" {
		f, err := ioutil.ReadFile("templates/dashboard.html")
		if err != nil {
			log.Println("Error while reading dashboard.html", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		fmt.Fprint(w, string(f))
	}
}

func history(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	exists, err := nameExists(name)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	if !exists {
		http.NotFound(w, r)
		return
	}

	set := client.ZRevRange(name, "0", "-1")
	if set.Err() != nil {
		log.Println("Failure to ZREVRANGE for", name, set.Err())
	}

	// chop off the last history member since it is the placeholder value from when the set was created
	vals := set.Val()[:len(set.Val())-1]

	history := make([]GeobinRequest, 0, len(vals))
	for _, v := range vals {
		var gr GeobinRequest
		if err := json.Unmarshal([]byte(v), &gr); err != nil {
			log.Println("Error unmarshalling request history:", err)
		}
		history = append(history, gr)
	}

	resp, err := json.Marshal(history)
	if err != nil {
		log.Println("Error marshalling request history:", err)
		http.Error(w, "Could not generate history.", http.StatusInternalServerError)
		return
	}

	fmt.Fprint(w, string(resp))
}

func openSocket(w http.ResponseWriter, r *http.Request) {
	// upgrade the connection
	name := mux.Vars(r)["name"]

	// start pub subbing
	if err := pubsub.Subscribe(name); err != nil {
		log.Println("Failure to SUBSCRIBE to", name, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s, err := socket.NewSocket(name, w, r)
	if err != nil {
		// if there is an error, NewSocket will have already written a response via http.Error()
		// so only write a log
		log.Println("Error opening websocket:", err)
		return
	}

	s.SetOnClose(func(n string) {
		if err := pubsub.Unsubscribe(name); err != nil {
			log.Println("Failure to UNSUBSCRIBE from", n, err)
		}
		delete(sockets, n)
	})

	// keep track of the outbound channel for pubsubbery
	sockets[name] = s
}

func redisPump() {
	for {
		v, err := pubsub.Receive()
		if err != nil {
			log.Println("Error from Redis PubSub:", err)
			return
		}

		switch v := v.(type) {
		case *redis.Message:
			s, ok := sockets[v.Channel]
			if !ok {
				log.Println("Got message for unknown channel:", v.Channel)
				return
			}
			log.Println("sending message to socket, you sucka")
			s.Write([]byte(v.Payload))
		}
	}
}

func randomString(length int) (string, error) {
	b := make([]byte, length)
	for i, _ := range b {
		b[i] = config.NameVals[rand.Intn(len(config.NameVals))]
	}

	s := string(b)

	exists, err := nameExists(s)
	if err != nil {
		log.Println("Failure to EXISTS for:", s, err)
		return "", err
	}

	if exists {
		return randomString(length)
	}

	return s, nil
}

func nameExists(name string) (bool, error) {
	resp := client.Exists(name)
	if resp.Err() != nil {
		return false, resp.Err()
	} else {
		if resp.Val() {
			return true, nil
		} else {
			return false, nil
		}
	}
}
