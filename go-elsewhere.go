package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

type MyProxy struct {
	forward httputil.ReverseProxy
}

var route = make(map[string]string)

func (p *MyProxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method == "CONFIG" {
		h := strings.Split(req.URL.Path, "/")[1]
		log.Printf("Config %s => %s", req.Host, h)
		route[req.Host] = h
		rw.WriteHeader(http.StatusOK)

		return
	}

	p.forward.ServeHTTP(rw, req)
}

func main() {
	var listen = flag.String("listen", ":80", "host:port to listen on")
	flag.Parse()

	director := func(req *http.Request) {
		req.URL.Scheme = "http"
		req.URL.Host = req.Host
	}

	dial := func(n, addr string) (net.Conn, error) {
		to := strings.Split(addr, ":")[0]
		log.Printf("Redirect %s => %s", addr, route[to])
		return net.DialTimeout(n, route[to], 200*time.Millisecond)
	}

	proxy := &MyProxy{
		forward: httputil.ReverseProxy{
			Director:  director,
			Transport: &http.Transport{Dial: dial},
		},
	}

	log.Fatal(http.ListenAndServe(*listen, proxy))
}
