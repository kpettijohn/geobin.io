package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/geoloqi/geobin-go/manager"
	"github.com/geoloqi/geobin-go/socket"
	redis "github.com/vmihailenco/redis/v2"
)

var config = &Config{}
var client = &redis.Client{}
var pubsub = &redis.PubSub{}
var socketManager = manager.NewManager(make(map[string]map[string]socket.S))

// starts the redis pump and http server
func main() {
	// set numprocs
	runtime.GOMAXPROCS(runtime.NumCPU())
	// add file info to log statements
	log.SetFlags(log.Ldate | log.Ltime | log.Llongfile)
	// set up unique seed for random num generation
	rand.Seed(time.Now().UTC().UnixNano())

	// prepare router
	r := createRouter()
	http.Handle("/", r)

	loadConfig()
	setupRedis()

	// loop for receiving messages from Redis pubsub, and forwarding them on to relevant ws connection
	go redisPump()

	defer func() {
		pubsub.Close()
		client.Close()
	}()

	// Start up HTTP server
	fmt.Fprintf(os.Stdout, "Starting server at %v:%d\n", config.Host, config.Port)
	err := http.ListenAndServe(fmt.Sprintf("%v:%d", config.Host, config.Port), nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

// setupRedis creates a redis client and pubsub
func setupRedis() {
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

// redisPump reads messages out of redis and pushes them through the
// appropriate websocket
func redisPump() {
	for {
		v, err := pubsub.Receive()
		if err != nil {
			log.Println("Error from Redis PubSub:", err)
			return
		}

		switch v := v.(type) {
		case *redis.Message:
			var sockMap map[string]socket.S
			var ok bool
			manageSockets(func(sockets map[string]map[string]socket.S) {
				sockMap, ok = sockets[v.Channel]
			})

			if !ok {
				log.Println("Got message for unknown channel:", v.Channel)
				return
			}

			for _, sock := range sockMap {
				go func(s socket.S, p []byte) {
					s.Write(p)
				}(sock, []byte(v.Payload))
			}
		}
	}
}
