// vim: noet:ai:sw=8
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/gbarr/gouuid"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
	"time"
)

var route = make(map[string]string)

var subdomain = flag.Bool("allow-subdomains", false, "Allow sub-domains")
var listen = flag.String("listen", ":80", "host:port to listen on")
var configAuth = flag.String("config-auth", "", "require auth to config")
var proxyAuth = flag.String("proxy-auth", "", "require auth to proxy")
var uid = flag.String("uid", "", "UID to prevent loops")
var pairServer = flag.String("pair-server", "", "host:port to pair server to relay config settings")

type MyProxy struct {
	httputil.ReverseProxy
}

type MyTransport struct {
	http.Transport
}

type dummyResponse struct {
	hdr http.Header
}

func (w *dummyResponse) Header() http.Header {
	return w.hdr
}

func (w *dummyResponse) WriteHeader(code int) {
	return
}

func (w *dummyResponse) Write(data []byte) (n int, err error) {
	return 0, nil
}

func checkLoop(req *http.Request) error {
	for _, value := range req.Header["X-Elsewhere"] {
		if value == *uid {
			return fmt.Errorf("Loop detected %s => %v", req.Host, req.Header["X-Elsewhere"])
		}
	}
	req.Header.Add("X-Elsewhere", *uid)
	return nil
}

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

func checkAuth(req *http.Request, field string, b64 *string) bool {
	if len(*b64) == 0 {
		return true
	}
	return *b64 == req.Header.Get(field)
}

func mirrorConfig(p *MyProxy, req *http.Request) {
	if len(*pairServer) > 0 {
		rw := &dummyResponse{hdr: http.Header{}}
		req.URL.Host = *pairServer
		p.ReverseProxy.ServeHTTP(rw, req)
	}
}

func (p *MyProxy) ServeHTTPConfig(rw http.ResponseWriter, req *http.Request) {
	if checkLoop(req) != nil {
		rw.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !checkAuth(req, "Authorization", configAuth) {
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch req.Method {
	case "GET":
		str, _ := json.Marshal(&route)
		rw.Header().Set("Content-Length", strconv.Itoa(len(str)))
		rw.Write(str)
	case "PUT":
		h := strings.Split(req.URL.Path, "/")[1]
		log.Printf("Config %s => %s", req.Host, h)
		route[req.Host] = h
		mirrorConfig(p, req)
		rw.WriteHeader(http.StatusOK)
	case "DELETE":
		if _, found := route[req.Host]; found {
			log.Printf("Clear %s", req.Host)
			delete(route, req.Host)
			mirrorConfig(p, req)
			rw.WriteHeader(http.StatusOK)
		} else {
			log.Printf("No such mapping %s", req.Host)
			rw.WriteHeader(http.StatusNotFound)
		}
	default:
		rw.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (p *MyProxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if len(req.Header.Get("X-Elsewhere-Config")) > 0 {
		p.ServeHTTPConfig(rw, req)
		return
	}
	if !checkAuth(req, "Proxy-Authorization", proxyAuth) {
		rw.WriteHeader(http.StatusProxyAuthRequired)
		return
	}
	err := checkLoop(req)
	if err == nil {
		err = mapRequest(req)
	}
	if err != nil {
		log.Printf("%v", err)
		rw.WriteHeader(http.StatusServiceUnavailable)
	} else {
		p.ReverseProxy.ServeHTTP(rw, req)
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

	if len(*uid) == 0 {
		uuid4, _ := uuid.NewV4()
		*uid = uuid4.String()
	}

	if len(*pairServer) > 0 {
		req, _ := http.NewRequest("GET", "http://"+*pairServer+"/", nil)
		req.Header.Set("X-Elsewhere-Config", "1")
		if len(*configAuth) > 0 {
			req.Header.Set("Authorization", *configAuth)
		}
		resp, err := http.DefaultClient.Do(req)

		if err == nil {
			if resp.StatusCode != http.StatusOK {
				log.Printf("Failed to get pair config [%s] %s", *pairServer, resp.Status)
				os.Exit(1)
			}
			rbody := resp.Body
			if rbody != nil {
				var bout bytes.Buffer
				io.Copy(&bout, rbody)
				rbody.Close()
				if bout.Len() > 0 {
					json.Unmarshal(bout.Bytes(), &route)
					log.Printf("CONFIG = %s", bout.String())
				}
			}

		}
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
