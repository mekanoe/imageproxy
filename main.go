package main

import (
	"bytes"
	"image"
	"image/draw"
	"log"
	"os"
	"strconv"

	"image/gif"
	"image/jpeg"
	"image/png"

	_ "github.com/kayteh/esi"
	"github.com/muesli/smartcrop"
	"github.com/nfnt/resize"
	"github.com/valyala/fasthttp"
	_ "golang.org/x/image/webp"
)

var (
	AcceptedEncodings = []string{"jpeg", "png", "gif"}
)

type proxyReq struct {
	Proto []byte
	Host  []byte
	Path  []byte
}

type proxy struct {
	client fasthttp.Client
	sc     smartcrop.Analyzer
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":6345"
	}

	p := &proxy{
		client: fasthttp.Client{},
		sc:     smartcrop.NewAnalyzer(),
	}
	// for i := 0; i < 10; i++ {
	// 	// p.client.Clients = append(p.client.Clients, &fasthttp.Client{})
	// }

	srv := fasthttp.Server{
		Handler: p.handle,
	}

	log.Fatalln(srv.ListenAndServe(addr))
}

func (p *proxy) handle(ctx *fasthttp.RequestCtx) {
	parts := bytes.Split(ctx.URI().Path(), []byte("/"))

	// /1920x1080/https://i.redd.it/ivm6xbxgqbgz.jpg
	if len(parts) < 5 {
		ctx.Error("url too short.", 400)
		return
	}

	parts = parts[1:]

	pr := proxyReq{
		Proto: parts[1],
		Host:  parts[2],
		Path:  bytes.Join(parts[3:], []byte("/")),
	}

	resp, err := p.fetchURL(pr)
	if err != nil {
		ctx.Error("request failed", 502)
		return
	}

	img, err := p.getImageFromResponse(resp)
	if err != nil {
		ctx.Error("body wasn't an image", 500)
		return
	}

	var outImg image.Image
	if !bytes.Equal(parts[0], []byte("x")) {
		// if first part defines a size, we do stuff.

		bimg := img
		or := p.getOutputRect(parts[0])

		bimg = p.smartCrop(bimg, or)
		bimg = p.resize(bimg, or)

		outImg = bimg
	} else {
		outImg = img
	}

	w := ctx.Response.BodyWriter()

	// decide what output encoder
	encoder := p.getEncoder(ctx.Request.URI().QueryArgs(), ctx.Request.Header)

	switch encoder {
	case "jpeg":
		jpeg.Encode(w, outImg, &jpeg.Options{
			Quality: 80,
		})
	case "png":
		png.Encode(w, outImg)
	case "gif":
		gif.Encode(w, outImg, nil)
	}

	ctx.Response.Header.SetContentType("image/" + encoder)
}

func (p *proxy) fetchURL(r proxyReq) (*fasthttp.Response, error) {
	req := fasthttp.AcquireRequest()

	req.Header.SetHostBytes(r.Host)
	req.Header.SetMethod("GET")
	req.Header.SetRequestURIBytes(r.Path)

	resp := fasthttp.AcquireResponse()

	err := p.client.Do(req, resp)
	return resp, err
}

func (p *proxy) getImageFromResponse(r *fasthttp.Response) (image.Image, error) {
	buf := bytes.NewBuffer(r.Body())

	img, _, err := image.Decode(buf)
	return img, err
}

func (p *proxy) getOutputRect(part []byte) image.Rectangle {
	// 100x100 => 100, 100
	size := bytes.Split(part, []byte("x"))
	ix, _ := strconv.Atoi(string(size[0]))
	iy, _ := strconv.Atoi(string(size[1]))
	return image.Rect(0, 0, ix, iy)
}

func (p *proxy) dumbCrop(img image.Image, or image.Rectangle) image.Image {
	bounds := img.Bounds()
	ix := or.Max.X
	iy := or.Max.Y

	// get middle
	mx := bounds.Max.X / 2
	my := bounds.Max.Y / 2

	tx := (ix / 2) - mx
	ty := (iy / 2) - my

	cropRect := image.Rect(tx, ty, mx+ix, mx+iy)
	outImg := image.NewRGBA(or)
	draw.Draw(outImg, cropRect, img, cropRect.Min, draw.Src)

	return outImg
}

func (p *proxy) resize(img image.Image, or image.Rectangle) image.Image {
	b := img.Bounds()

	ww := uint(or.Max.X)
	hh := uint(or.Max.Y)

	if b.Max.X > b.Max.Y {
		hh = 0
	}

	if b.Max.X < b.Max.Y {
		ww = 0
	}

	return resize.Resize(ww, hh, img, resize.Lanczos3)
}

func (p *proxy) smartCrop(img image.Image, or image.Rectangle) image.Image {
	r, err := p.sc.FindBestCrop(img, or.Dx(), or.Dy())
	if err != nil {
		log.Println("smartcrop err", err)
		return img
	}

	return p.dumbCrop(img, r)
}

func (p *proxy) getEncoder(qa *fasthttp.Args, h fasthttp.RequestHeader) string {
	for _, e := range AcceptedEncodings {
		if h.HasAcceptEncoding("image/" + e) {
			return e
		}

		if bytes.Equal(qa.Peek("o"), []byte(e)) {
			return e
		}
	}

	return "jpeg"
}
