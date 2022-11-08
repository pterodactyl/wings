package transfer

import (
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
)

func (t *Transfer) StreamExtraction(url, token string) error {
	client := http.Client{Timeout: 0}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", token)

	res, err := client.Do(req)
	if err != nil {
		return err
	}

	mediaType, params, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil {
		return err
	}

	if !strings.HasPrefix(mediaType, "multipart/") {
		return fmt.Errorf("invalid content type \"%s\", expected \"multipart/form-data\"", mediaType)
	}

	mr := multipart.NewReader(res.Body, params["boundary"])
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		fmt.Println(p)
	}

	return nil
}
