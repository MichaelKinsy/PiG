package transportperrequest

import "net/http"

func fetch(url string) (*http.Response, error) {
	c := &http.Client{Transport: &http.Transport{}} // want `fetch builds an http.Transport`
	return c.Get(url)
}

func newClient() *http.Client { return &http.Client{Transport: &http.Transport{}} }

var shared = &http.Transport{}
