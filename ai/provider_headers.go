package ai

import (
	"net/http"
	"strings"
)

func applyProviderHeaders(request *http.Request, headers ProviderHeaders) {
	for name, value := range headers {
		if strings.EqualFold(name, "host") {
			if value == nil {
				request.Host = ""
			} else {
				request.Host = *value
			}
			continue
		}
		if value == nil {
			request.Header.Del(name)
			continue
		}
		request.Header.Set(name, *value)
	}
}
