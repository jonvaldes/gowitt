package main

/*
#cgo pkg-config: glib-2.0 cairo pangocairo
#include <cairo/cairo.h>
*/
import "C"

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

import _ "image/jpeg"

const DownloadGoroutines = 5

type CacheNode struct {
	LastUsed    int64
	Img         *C.cairo_surface_t
	imgInternal image.Image
	Filename    string
}

type ImageInfo struct {
	URL         string
	Filename    string
	Img         *C.cairo_surface_t
	imgInternal image.Image
}

type ImageCache struct {
	sync.Mutex
	Cache map[string]CacheNode

	URLRequests chan string
	Downloads   chan ImageInfo
}

func loadImage(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		return nil, err
	}

	// Convert image to ARGB format
	newimg := image.NewRGBA(img.Bounds())
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			newimg.Set(x, y, color.RGBA{uint8(b >> 8), uint8(g >> 8), uint8(r >> 8), uint8(a >> 8)}) // Convert to ARGB, as Cairo expects
		}
	}
	return newimg, nil
}

func loadCairoImage(img image.Image) *C.cairo_surface_t {
	i := img.(*image.RGBA)
	loadedImage := C.cairo_image_surface_create_for_data(
		(*C.uchar)(&i.Pix[0]),
		C.cairo_format_t(C.CAIRO_FORMAT_ARGB32),
		C.int(img.Bounds().Dx()), C.int(img.Bounds().Dy()), C.int(i.Stride))

	if C.cairo_surface_status(loadedImage) != C.CAIRO_STATUS_SUCCESS {
		fmt.Println("COuld not create cairo image", C.GoString(C.cairo_status_to_string(C.cairo_surface_status(loadedImage))))
		return nil
	}
	return loadedImage
}

func imageDownloader(URLs <-chan string, files chan<- ImageInfo) {
	for {
		URL := <-URLs

		info := ImageInfo{
			URL:      URL,
			Filename: URLToFilename(URL),
			Img:      nil,
		}

		// Check hard drive
		img, err := loadImage(info.Filename)
		if err == nil {
			info.Img = loadCairoImage(img)
			info.imgInternal = img
			files <- info
			continue
		}

		fmt.Println("Downloading", info.URL)
		delay := time.Second / 2
		retriesLeft := 3
	retry:
		delay *= 2
		if retriesLeft == 0 {
			// TODO -log failure
			fmt.Println("Retries exhausted trying to download", info.URL)
			continue
		}
		retriesLeft--
		resp, err := http.Get(info.URL)
		if err != nil {
			time.Sleep(delay)
			goto retry
		}

		// Download image to buffer
		var buf bytes.Buffer

		_, err = io.Copy(&buf, resp.Body)
		resp.Body.Close()
		if err != nil {
			time.Sleep(delay)
			fmt.Println("error downloading image")
			goto retry
		}

		// Make sure it's a png image
		img, _, err = image.Decode(&buf)
		if err != nil {
			fmt.Println("error decoding image")
			time.Sleep(delay)
			goto retry
		}

		// Save image to disk
		file, err := os.Create(info.Filename)
		if err != nil {
			panic(err) // TODO -- handle this gracefully
		}

		if err := png.Encode(file, img); err != nil {
			panic(err) // TODO -- handle this gracefully
		}

		file.Close()

		img, err = loadImage(info.Filename)
		if err != nil {
			panic("Can't load png image" + info.Filename) // TODO -- do something
		}
		info.Img = loadCairoImage(img)
		info.imgInternal = img
		files <- info
	}
}

func imageAdder(ic *ImageCache) {
	for {
		info := <-ic.Downloads

		ic.Lock()
		ic.Cache[info.URL] = CacheNode{
			LastUsed:    0,
			Img:         info.Img,
			imgInternal: info.imgInternal,
			Filename:    info.Filename,
		}
		ic.Unlock()

		fmt.Println("Added", info.URL)
		// TODO -- trigger redraw
		// TODO -- keep track of when was each image used
		// TODO -- remove oldest-used images when cache fills up
	}
}

func NewImageCache() *ImageCache {

	var Result ImageCache
	Result.URLRequests = make(chan string, 20)
	Result.Downloads = make(chan ImageInfo, 20)
	Result.Cache = make(map[string]CacheNode)

	for i := 0; i < DownloadGoroutines; i++ {
		go imageDownloader(Result.URLRequests, Result.Downloads)
	}

	go imageAdder(&Result)

	return &Result
}

func URLToFilename(URL string) string {
	hash := sha1.Sum([]byte(URL))
	base := base64.URLEncoding.EncodeToString(hash[:])
	base = strings.Replace(base, "=", "_", -1)
	return fmt.Sprintf("/home/juanval/gowitt/images/%s.png", base)
}

func GetCachedImage(ic *ImageCache, URL string) *C.cairo_surface_t {

	// Check if image already in cache
	ic.Lock()
	img, ok := ic.Cache[URL]
	ic.Unlock()

	if ok {
		return img.Img
	}

	// If not in cache, request it
	ic.URLRequests <- URL

	return nil
}
