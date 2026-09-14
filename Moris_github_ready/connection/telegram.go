package connection

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Client struct {
	Token string
	Base  string
	HTTP  *http.Client
}

type response struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
}

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}
type Message struct {
	MessageID int64       `json:"message_id"`
	Chat      Chat        `json:"chat"`
	From      *User       `json:"from,omitempty"`
	Text      string      `json:"text,omitempty"`
	Document  *Document   `json:"document,omitempty"`
	Video     *Media      `json:"video,omitempty"`
	Audio     *Media      `json:"audio,omitempty"`
	Animation *Media      `json:"animation,omitempty"`
	Voice     *Media      `json:"voice,omitempty"`
	Photo     []PhotoSize `json:"photo,omitempty"`
}
type Chat struct {
	ID int64 `json:"id"`
}
type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name,omitempty"`
	Username  string `json:"username,omitempty"`
}
type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
}
type Media struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
}
type PhotoSize struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size,omitempty"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}
type File struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size,omitempty"`
	FilePath string `json:"file_path,omitempty"`
}

func New(token, base string) *Client {
	return &Client{Token: token, Base: base, HTTP: &http.Client{Timeout: 30 * time.Minute}}
}

func (c *Client) call(ctx context.Context, method string, v url.Values, out any) error {
	endpoint := fmt.Sprintf("%s/bot%s/%s", c.Base, c.Token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var r response
	if err = json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return err
	}
	if !r.OK {
		return fmt.Errorf("telegram API %d: %s", r.ErrorCode, r.Description)
	}
	if out != nil && len(r.Result) > 0 {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func (c *Client) GetUpdates(ctx context.Context, offset int64) ([]Update, error) {
	v := url.Values{"offset": {strconv.FormatInt(offset, 10)}, "timeout": {"50"}, "limit": {"100"}}
	var out []Update
	return out, c.call(ctx, "getUpdates", v, &out)
}

func (c *Client) GetFile(ctx context.Context, fileID string) (*File, error) {
	v := url.Values{"file_id": {fileID}}
	var out File
	return &out, c.call(ctx, "getFile", v, &out)
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	v := url.Values{"chat_id": {strconv.FormatInt(chatID, 10)}, "text": {text}}
	return c.call(ctx, "sendMessage", v, nil)
}

func (c *Client) DownloadFile(ctx context.Context, path string, dst io.Writer) error {
	u := fmt.Sprintf("%s/file/bot%s/%s", c.Base, c.Token, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram download HTTP %s", resp.Status)
	}
	_, err = io.Copy(dst, resp.Body)
	return err
}
