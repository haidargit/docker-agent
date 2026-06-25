package leantui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	"image/png"
	"strings"

	"github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/tools"
)

const (
	kittyMaxChunkSize = 4096
	kittyMaxImageCols = 80
	kittyMaxImageRows = 30
)

type inlineImage struct {
	name    string
	mime    string
	pngData []byte
	width   int
	height  int
}

func inlineImagesFromToolResult(result *tools.ToolCallResult) []inlineImage {
	if result == nil {
		return nil
	}

	images := make([]inlineImage, 0, len(result.Images)+len(result.Documents))
	for i, img := range result.Images {
		name := fmt.Sprintf("image-%d", i+1)
		if inline, ok := inlineImageFromBase64(name, img.MimeType, img.Data); ok {
			images = append(images, inline)
		}
	}
	for _, doc := range result.Documents {
		if !chat.IsImageMimeType(doc.MimeType) || doc.Data == "" {
			continue
		}
		name := doc.Name
		if name == "" {
			name = "image"
		}
		if inline, ok := inlineImageFromBase64(name, doc.MimeType, doc.Data); ok {
			images = append(images, inline)
		}
	}
	return images
}

func inlineImageFromBase64(name, mimeType, b64 string) (inlineImage, bool) {
	if strings.TrimSpace(b64) == "" {
		return inlineImage{}, false
	}

	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return inlineImage{}, false
	}
	if mimeType == "" {
		mimeType = chat.DetectMimeTypeByContent(data)
	}
	if !chat.IsImageMimeType(mimeType) {
		return inlineImage{}, false
	}
	if resized, err := chat.ResizeImage(data, mimeType); err == nil {
		data = resized.Data
		mimeType = resized.MimeType
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return inlineImage{}, false
	}
	bounds := img.Bounds()

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return inlineImage{}, false
	}

	return inlineImage{
		name:    name,
		mime:    mimeType,
		pngData: pngBuf.Bytes(),
		width:   bounds.Dx(),
		height:  bounds.Dy(),
	}, true
}

func renderInlineImage(img inlineImage, width int) []string {
	if len(img.pngData) == 0 || img.width <= 0 || img.height <= 0 {
		return nil
	}

	cols := min(max(width-4, 1), kittyMaxImageCols)
	rows := max(1, (img.height*cols+img.width-1)/img.width/2)
	rows = min(rows, kittyMaxImageRows)

	label := "image"
	if img.name != "" {
		label = img.name
	}
	if img.mime != "" {
		label += " (" + img.mime + ")"
	}

	out := []string{"  " + stMuted().Render("🖼 "+label)}
	out = append(out, "  "+kittyImageSequence(img.pngData, cols, rows))
	for range rows - 1 {
		out = append(out, "")
	}
	return out
}

func kittyImageSequence(pngData []byte, cols, rows int) string {
	encoded := base64.StdEncoding.EncodeToString(pngData)
	var b strings.Builder
	for offset := 0; offset < len(encoded); offset += kittyMaxChunkSize {
		end := min(offset+kittyMaxChunkSize, len(encoded))
		more := 0
		if end < len(encoded) {
			more = 1
		}
		if offset == 0 {
			fmt.Fprintf(&b, "\x1b_Ga=T,t=d,f=100,q=2,C=1,c=%d,r=%d,m=%d;%s\x1b\\", cols, rows, more, encoded[offset:end])
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, encoded[offset:end])
		}
	}
	return b.String()
}
