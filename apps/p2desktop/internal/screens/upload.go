package screens

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// Upload is a file the server holds for a session: the ID goes to Chat.Send's attachments.
type Upload struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType"`
}

// UploadFile sends the file at path to the session's uploads (`POST /upload` of the server at origin, as the web's + does:
// the session's files answer the server alone, so no surface can reach them). The request is the local owner's: same
// origin, with its session cookie (loopback alone is nobody).
func UploadFile(ctx context.Context, origin, cookie, session, path string) (Upload, error) {
	file, err := os.Open(path)
	if err != nil {
		return Upload{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Upload{}, err
	}
	if info.IsDir() {
		return Upload{}, fmt.Errorf("%s is a folder", filepath.Base(path))
	}
	query := url.Values{"session": {session}, "name": {filepath.Base(path)}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/upload?"+query.Encode(), file)
	if err != nil {
		return Upload{}, err
	}
	request.ContentLength = info.Size()
	request.Header.Set("Origin", origin)
	request.Header.Set("Cookie", cookie)
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return Upload{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Upload{}, err
	}
	if response.StatusCode != http.StatusOK {
		var refused struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &refused) == nil && refused.Error != "" {
			return Upload{}, fmt.Errorf("%s: %s", filepath.Base(path), refused.Error)
		}
		return Upload{}, fmt.Errorf("%s: upload answered %d", filepath.Base(path), response.StatusCode)
	}
	var uploaded Upload
	if err := json.Unmarshal(body, &uploaded); err != nil || uploaded.ID == "" {
		return Upload{}, fmt.Errorf("%s: unreadable upload answer", filepath.Base(path))
	}
	return uploaded, nil
}
