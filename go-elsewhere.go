package main

import (
	"bytes"
	"encoding/base64"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

var subdomain = flag.Bool("allow-subdomains", false, "Allow sub-domains")
var listen = flag.String("listen", ":80", "host:port to listen on")
var configAuth = flag.String("config-auth", "", "require auth to config")
var proxyAuth = flag.String("proxy-auth", "", "require auth to proxy")

type MyProxy struct {
	httputil.ReverseProxy
}

type MyTransport struct {
	http.Transport
}

var route = make(map[string]string)

func mapRequest(req *http.Request) error {

	for _, value := range req.Header["X-Elsewhere"] {
		if value == *listen {
			return fmt.Errorf("Loop detected %s => %v", req.Host, req.Header["X-Elsewhere"])
		}
	}
	req.Header.Add("X-Elsewhere", *listen)

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

func checkAuth(req *http.Request, field string, b64 *string) bool {
	if len(*b64) == 0 {
		return true
	}
	return *b64 == req.Header.Get(field)
}

func (p *MyProxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case "CONFIG":
		if !checkAuth(req, "Authorization", configAuth) {
			rw.WriteHeader(http.StatusUnauthorized)
			return
		}
		h := strings.Split(req.URL.Path, "/")[1]
		log.Printf("Config %s => %s", req.Host, h)
		route[req.Host] = h
		rw.WriteHeader(http.StatusOK)
	case "CLEAR":
		if !checkAuth(req, "Authorization", configAuth) {
			rw.WriteHeader(http.StatusUnauthorized)
			return
		}
		if _, found := route[req.Host]; found {
			log.Printf("Clear %s", req.Host)
			delete(route, req.Host)
			rw.WriteHeader(http.StatusOK)
		} else {
			log.Printf("No such mapping %s", req.Host)
			rw.WriteHeader(http.StatusNotFound)
		}
	default:
		if !checkAuth(req, "Proxy-Authorization", proxyAuth) {
			rw.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		if err := mapRequest(req); err != nil {
			log.Printf("%v", err)
			rw.WriteHeader(http.StatusServiceUnavailable)
		} else {
			p.ReverseProxy.ServeHTTP(rw, req)
		}
	}
}

func (t *MyTransport) RoundTrip(req *http.Request) (res *http.Response, err error) {
	res, err = t.Transport.RoundTrip(req)
	if err != nil {
		log.Printf("%v", err)
		res = &http.Response{
			StatusCode:    503,
			ProtoMajor:    1,
			ProtoMinor:    0,
			Header:        http.Header{},
			Body:          ioutil.NopCloser(bytes.NewBufferString("")),
			ContentLength: 0,
		}
		err = nil
	}
	return res, err
}

func main() {
	flag.Parse()

	if len(*configAuth) > 0 {
		*configAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(*configAuth))
	}

	if len(*proxyAuth) > 0 {
		*proxyAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(*proxyAuth))
	}

	director := func(req *http.Request) {
		req.URL.Scheme = "http"
	}

	dial := func(n, addr string) (net.Conn, error) {
		return net.DialTimeout(n, addr, 200*time.Millisecond)
	}

	proxy := &MyProxy{
		httputil.ReverseProxy{
			Director: director,
			Transport: &MyTransport{
				http.Transport{
					Dial: dial,
				},
			},
		},
	}

	log.Fatal(http.ListenAndServe(*listen, proxy))
}
