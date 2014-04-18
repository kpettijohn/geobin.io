package socket

import (
	"github.com/geoloqi/geobin-go/test"
	"testing"
	"net/http/httptest"
	"net/http"
	"time"
	"runtime"
	"sync"
	"fmt"
	"sync/atomic"
)

func TestRoundTrip(t *testing.T) {
	msgReceived := false
	ts := makeSocketServer(t, "test_socket", func(messageType int, message []byte) {
		msgReceived = true
		test.Expect(t, string(message), "You got a message!")
	}, nil)
	roundTrip(t, ts, "test_client")
	test.Expect(t, msgReceived, true)
}

func TestManyRoundTrips(t *testing.T) {
	runtime.GOMAXPROCS(runtime.NumCPU())
	defer runtime.GOMAXPROCS(1)
	var msgCount uint64 = 0
	ts := makeSocketServer(t, "test_socket", func(messageType int, message []byte) {
		atomic.AddUint64(&msgCount, 1)
		test.Expect(t, string(message), "You got a message!")
	}, nil)
	defer ts.Close()

	count := 100
	var w sync.WaitGroup
	w.Add(count)
	for i := 0; i < count; i++ {
		go func(index int) {
			roundTrip(t, ts, fmt.Sprint("test_client:", index))
			w.Done()
		}(i)
	}
	w.Wait()
	test.Expect(t, msgCount, uint64(count))
}

func TestOnClose(t *testing.T) {
	serverClosed := false
	ts := makeSocketServer(t, "test_socket", nil, func(name string) {
		serverClosed = true
		test.Expect(t, name, "test_socket")
	})
	defer ts.Close()

	clientClosed := false
	client := makeClient(t, ts.URL, "test_client", nil, func(name string) {
		clientClosed = true
		test.Expect(t, name, "test_client")
	})

	client.Close()

	time.Sleep(100 * time.Microsecond)
	test.Expect(t, clientClosed, true)
	test.Expect(t, serverClosed, true)
}

func makeSocketServer(t *testing.T, name string, or func(int, []byte), oc func(string)) (*httptest.Server){
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sck, err := NewSocket(name, w, r)
		if err != nil {
			t.Error("Error creating websocket:", err)
		}

		sck.SetOnRead(or)
		sck.SetOnClose(oc)

		sck.Write([]byte("You got a message!"))
	}))

	return ts
}

func makeClient(t *testing.T, url string, name string, or func(int, []byte), oc func(string)) S {
	client, err := NewClient(name, url)
	if err != nil {
		t.Error("Error opening client socket:", name, err)
	}

	client.SetOnRead(or)
	client.SetOnClose(oc)
	return client
}

func roundTrip(t *testing.T, ts *httptest.Server, clientName string) {
	msgReceived := false
	client := makeClient(t, ts.URL, clientName, func(messageType int, message []byte) {
		msgReceived = true
		test.Expect(t, string(message), "You got a message!")
	}, nil)
	client.Write([]byte("You got a message!"))

	// sleep a lil bit to allow the server to write back to the websocket
	time.Sleep(25 * time.Millisecond)
	test.Expect(t, msgReceived, true)
}
