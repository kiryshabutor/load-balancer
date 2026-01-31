package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

type ServerPool struct {
	servers []*url.URL
	current uint64
}

func NewServerPool(servers []string) *ServerPool {
	pool := &ServerPool{
		servers: make([]*url.URL, len(servers)),
		current: 0,
	}
	for i, s := range servers {
		url, err := url.Parse(s)
		if err != nil {
			panic(err)
		}
		pool.servers[i] = url
	}
	return pool
}

func (s *ServerPool) AddPeer(server string) {
	url, err := url.Parse(server)
	if err != nil {
		panic(err)
	}
	s.servers = append(s.servers, url)
}

func (s *ServerPool) GetNextPeer() *url.URL {
	next := atomic.AddUint64(&s.current, 1) % uint64(len(s.servers))
	server := s.servers[next]
	return server
}

func loadBalancerHandler(pool *ServerPool, w http.ResponseWriter, r *http.Request) {
	peer := pool.GetNextPeer()
	log.Printf("Forwarding request to %s", peer.Host)	

	proxy := httputil.NewSingleHostReverseProxy(peer)

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("[%s] connection failed: %v", peer.Host, err)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Service unavailable"))
	}

	proxy.ServeHTTP(w, r)
}

func launchMiniServer(port string) {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		defer r.Body.Close()

		log.Printf("Service on port %s received request: %s", port, string(body))

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Request from service on port %s\n", port)
	})

	log.Printf("Starting mini server on port %s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func main() {
	ports := []string{"8081", "8082", "8083"}

	for _, port := range ports {
		go launchMiniServer(port)
	}

	time.Sleep(time.Second)

	var servers []string
	for _, port := range ports {
		servers = append(servers, "http://localhost:"+port)
	}

	pool := NewServerPool(servers)

	server := http.Server{
		Addr: ":8080",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			loadBalancerHandler(pool, w, r)
		}),
	}

	log.Printf("Starting load balancer on port %s", server.Addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}
	log.Println("Server exited")
}
