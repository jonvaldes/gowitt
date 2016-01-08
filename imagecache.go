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
	"image/png"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

import _ "image/jpeg"

const DownloadGoroutines = 5

type CacheNode struct {
	LastUsed int64
	Img      *C.cairo_surface_t
	Filename string
}

type ImageCache struct {
	sync.Mutex
	Cache map[string]CacheNode

	URLRequests chan string
	Downloads   chan [2]string
}

func imageDownloader(URLs <-chan string, files chan<- [2]string) {
	for {
		URL := <-URLs

		hash := sha1.Sum([]byte(URL))
		outputFilename := "images/" + base64.URLEncoding.EncodeToString(hash[:]) + ".png"
		delay := time.Second / 2
		retriesLeft := 3
	retry:
		delay *= 2
		if retriesLeft == 0 {
			// TODO -log failure
			continue
		}
		retriesLeft--
		resp, err := http.Get(URL)
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
			goto retry
		}

		// Make sure it's a png image
		img, _, err := image.Decode(&buf)
		if err != nil {
			time.Sleep(delay)
			goto retry
		}

		// Save image to disk
		file, err := os.Create(outputFilename)
		if err != nil {
			panic(err) // TODO -- handle this gracefully
		}

		if err := png.Encode(file, img); err != nil {
			panic(err) // TODO -- handle this gracefully
		}

		file.Close()
		files <- [2]string{URL, outputFilename}
	}
}

func imageAdder(ic *ImageCache) {
	for {
		data := <-ic.Downloads
		URL := data[0]
		fileName := data[1]

		ic.Lock()
		ic.Cache[URL] = CacheNode{
			LastUsed: 0,
			Img:      nil,
			Filename: fileName,
		}
		ic.Unlock()

		fmt.Println("Added", URL, fileName)
		// TODO -- trigger redraw
	}
}

func NewImageCache() *ImageCache {

	var Result ImageCache
	Result.URLRequests = make(chan string, 20)
	Result.Downloads = make(chan [2]string, 20)
	Result.Cache = make(map[string]CacheNode)

	for i := 0; i < DownloadGoroutines; i++ {
		go imageDownloader(Result.URLRequests, Result.Downloads)
	}

	go imageAdder(&Result)

	return &Result
}

// The idea I have for this is that the image caching system launches
// a few goroutines that downloads missing user images to disk, then
// keeps them in a map that keeps a max number of images.
// Once you go over that limit, it removes them from the map.
// I'll have to decide how to handle old images so that you get the
// updated user images when people change them.
// Once a requested image is downloaded, it should trigger a redraw so
// the UI is updated immediately with new images

func GetCachedImage(ic *ImageCache, URL string) *C.cairo_surface_t {
	ic.Lock()
	img, ok := ic.Cache[URL]
	ic.Unlock()

	if ok {
		fmt.Println(img)
		// TODO -- return actual cached image
		return nil
	}

	// Trigger download
	ic.URLRequests <- URL

	// TODO -- return placeholder image
	return nil
}
