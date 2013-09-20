package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

var subdomain = flag.Bool("allow-subdomains", false, "Allow sub-domains")
var listen = flag.String("listen", ":80", "host:port to listen on")

type MyProxy struct {
	httputil.ReverseProxy
}

var route = make(map[string]string)

func mapRequest(req *http.Request) error {
	to := strings.Split(req.Host, ":")[0]
	for strings.Index(to, ".") > 0 {
		dest, ok := route[to]
		if ok {
			log.Printf("Redirect %s => %s", req.Host, dest)
			req.URL.Host = dest
			return nil
		}
		if *subdomain == false {
			break
		}
		to = strings.SplitAfterN(to, ".", 2)[1]
	}
	return fmt.Errorf("Redirect FAILED %s", req.Host)
}

func (p *MyProxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case "CONFIG":
		h := strings.Split(req.URL.Path, "/")[1]
		log.Printf("Config %s => %s", req.Host, h)
		route[req.Host] = h
		rw.WriteHeader(http.StatusOK)
	case "CLEAR":
		_, found := route[req.Host]
		if found {
			log.Printf("Clear %s", req.Host)
			delete(route, req.Host)
			rw.WriteHeader(http.StatusOK)
		} else {
			log.Printf("No such mapping %s", req.Host)
			rw.WriteHeader(http.StatusNotFound)
		}
	default:
		err := mapRequest(req)
		if err != nil {
			log.Printf("%v", err)
			rw.WriteHeader(http.StatusServiceUnavailable)
		} else {
			p.ReverseProxy.ServeHTTP(rw, req)
		}
	}
}

func main() {
	flag.Parse()

	director := func(req *http.Request) {
		req.URL.Scheme = "http"
	}

	dial := func(n, addr string) (net.Conn, error) {
		return net.DialTimeout(n, addr, 200*time.Millisecond)
	}

	proxy := &MyProxy{
		httputil.ReverseProxy{
			Director:  director,
			Transport: &http.Transport{Dial: dial},
		},
	}

	log.Fatal(http.ListenAndServe(*listen, proxy))
}
