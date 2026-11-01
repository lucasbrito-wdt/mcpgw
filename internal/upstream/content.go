package upstream

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// contentItem is the JSON shape of one non-text-only result item, as the script sees it.
type contentItem struct {
	Type        string           `json:"type"`
	Text        string           `json:"text,omitempty"`
	MimeType    string           `json:"mimeType,omitempty"`
	Data        string           `json:"data,omitempty"`
	URI         string           `json:"uri,omitempty"`
	Name        string           `json:"name,omitempty"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Size        *int64           `json:"size,omitempty"`
	Resource    *resourceContent `json:"resource,omitempty"`
	// Omitted explains a binary payload replaced to stay under the response size limit.
	Omitted string `json:"omitted,omitempty"`
}

// blobField returns the base64 payload field of the item, or nil when it has none.
func (it *contentItem) blobField() *string {
	switch {
	case it.Type == "image" || it.Type == "audio":
		return &it.Data
	case it.Resource != nil && it.Resource.Blob != "":
		return &it.Resource.Blob
	}
	return nil
}

type resourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// convertResult maps a tool result to the script-facing Value, never larger than limit bytes:
//   - structuredContent when present (it duplicates content in modern servers);
//   - only text: the joined text parsed as JSON when valid, else a JSON string;
//   - anything else: an array of typed items (text, image, audio, resource_link, resource).
//
// Base64 payloads count toward limit; ones that do not fit are replaced by a placeholder.
func convertResult(res *mcp.CallToolResult, limit int64) (Result, error) {
	if res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			return capped(Result{Value: b, Structured: true}, limit)
		}
	}
	if onlyText(res.Content) {
		return capped(textResult(textOf(res.Content)), limit)
	}
	items := make([]contentItem, 0, len(res.Content))
	for _, c := range res.Content {
		items = append(items, toItem(c))
	}
	fitBlobs(items, limit)
	b, err := json.Marshal(items)
	if err != nil {
		return Result{}, fmt.Errorf("encode tool result: %w", err)
	}
	return capped(Result{Value: b}, limit)
}

func capped(r Result, limit int64) (Result, error) {
	if limit > 0 && int64(len(r.Value)) > limit {
		return Result{}, ErrResponseTooLarge
	}
	return r, nil
}

func textResult(text string) Result {
	if text == "" {
		return Result{Value: json.RawMessage("null")}
	}
	if json.Valid([]byte(text)) {
		return Result{Value: json.RawMessage(text)}
	}
	b, _ := json.Marshal(text)
	return Result{Value: b}
}

func onlyText(content []mcp.Content) bool {
	for _, c := range content {
		if _, ok := c.(*mcp.TextContent); !ok {
			return false
		}
	}
	return true
}

func toItem(c mcp.Content) contentItem {
	switch v := c.(type) {
	case *mcp.TextContent:
		return contentItem{Type: "text", Text: v.Text}
	case *mcp.ImageContent:
		return binaryItem("image", v.MIMEType, v.Data)
	case *mcp.AudioContent:
		return binaryItem("audio", v.MIMEType, v.Data)
	case *mcp.ResourceLink:
		return contentItem{
			Type: "resource_link", URI: v.URI, Name: v.Name, Title: v.Title,
			Description: v.Description, MimeType: v.MIMEType, Size: v.Size,
		}
	case *mcp.EmbeddedResource:
		return embeddedItem(v.Resource)
	default:
		return contentItem{Type: "unsupported", Omitted: fmt.Sprintf("content type %T is not supported", c)}
	}
}

func binaryItem(kind, mimeType string, data []byte) contentItem {
	return contentItem{Type: kind, MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(data)}
}

func embeddedItem(r *mcp.ResourceContents) contentItem {
	if r == nil {
		return contentItem{Type: "resource", Omitted: "empty resource"}
	}
	rc := &resourceContent{URI: r.URI, MimeType: r.MIMEType, Text: r.Text}
	if r.Blob != nil {
		rc.Blob = base64.StdEncoding.EncodeToString(r.Blob)
	}
	return contentItem{Type: "resource", Resource: rc}
}

// fitBlobs keeps base64 payloads, in order, while the encoded result stays under limit, and
// replaces the rest with a placeholder.
func fitBlobs(items []contentItem, limit int64) {
	if limit <= 0 {
		return
	}
	fields := make([]*string, len(items))
	blobs := make([]string, len(items))
	for i := range items {
		if f := items[i].blobField(); f != nil {
			fields[i], blobs[i] = f, *f
			*f, items[i].Omitted = "", omittedNote(len(*f))
		}
	}
	skeleton, _ := json.Marshal(items)
	budget := limit - int64(len(skeleton))
	for i, f := range fields {
		if f == nil {
			continue
		}
		// Restoring adds `,"data":"<blob>"` and drops `,"omitted":"<note>"`.
		cost := int64(len(blobs[i])+len(`,"data":""`)) - int64(len(items[i].Omitted)+len(`,"omitted":""`))
		if cost > budget {
			continue
		}
		*f, items[i].Omitted = blobs[i], ""
		budget -= cost
	}
}

func omittedNote(size int) string {
	return fmt.Sprintf("binary payload of %d base64 bytes exceeds the response size limit", size)
}

func textOf(content []mcp.Content) string {
	var parts []string
	for _, c := range content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}

const maxToolErrorChars = 2000

// toolErrorMessage is the text of an isError result, or a compact form of its value.
func toolErrorMessage(res *mcp.CallToolResult, value json.RawMessage) string {
	if msg := textOf(res.Content); msg != "" {
		return msg
	}
	if len(value) == 0 || string(value) == "null" {
		return "the tool reported an error without details"
	}
	if len(value) > maxToolErrorChars {
		return string(value[:maxToolErrorChars]) + "..."
	}
	return string(value)
}
