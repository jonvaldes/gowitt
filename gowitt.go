package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ChimeraCoder/anaconda"
	"github.com/boltdb/bolt"
	"net/url"
	"strconv"
)

/*
#cgo pkg-config: glib-2.0 pango pangoxft
#cgo CFLAGS: -I/usr/include/freetype2
#cgo LDFLAGS: -lX11 -lXft
#include <X11/Xlib.h>
#include <X11/Xft/Xft.h>
#include <pango/pango.h>
#include <pango/pangoxft.h>
int getXEventType(XEvent e){ return e.type; }
XKeyEvent eventAsKeyEvent(XEvent e){ return e.xkey; }
int getDefaultScreen(Display * d){ return DefaultScreen(d); }
FcChar8* stringAsUtf8(const char * s){ return (FcChar8*)(s);}
*/
import "C"

type XWindow struct {
	Display         *C.Display
	Window          C.Window
	GraphicsContext C.GC
	// --
	FontDraw   *C.XftDraw
	ColorBlack C.XftColor
	// -- Pango
	PangoContext *C.PangoContext
	Layout       *C.PangoLayout
}

func CreateXWindow(width, height int) (XWindow, error) {
	var W XWindow

	W.Display = C.XOpenDisplay(nil)
	if W.Display == nil {
		return XWindow{}, errors.New("Can't open display")
	}
	W.Window = C.XCreateSimpleWindow(W.Display, C.XDefaultRootWindow(W.Display), 1, 1, C.uint(width), C.uint(height), 0, 0, 0xFFFFFF)
	C.XMapWindow(W.Display, W.Window)
	C.XFlush(W.Display)

	C.XSelectInput(W.Display, W.Window, C.ExposureMask|C.KeyPressMask|C.ButtonPressMask)

	// Create graphics context
	var valuemask C.ulong = C.GCCapStyle | C.GCJoinStyle
	var values C.XGCValues
	values.cap_style = C.CapButt
	values.join_style = C.JoinBevel
	W.GraphicsContext = C.XCreateGC(W.Display, C.Drawable(W.Window), valuemask, &values)
	if W.GraphicsContext == nil {
		return XWindow{}, errors.New("Could not create graphics context")
	}

	// Load Xft
	W.FontDraw = C.XftDrawCreate(W.Display, C.Drawable(W.Window), C.XDefaultVisual(W.Display, 0), C.XDefaultColormap(W.Display, 0))

	var color C.XRenderColor
	color.red = 0
	color.green = 0
	color.blue = 0
	color.alpha = 65535
	C.XftColorAllocValue(W.Display, C.XDefaultVisual(W.Display, 0), C.XDefaultColormap(W.Display, 0), &color, &W.ColorBlack)

	// Pango
	W.PangoContext = C.pango_xft_get_context(W.Display, 0)
	W.Layout = C.pango_layout_new(W.PangoContext)
	FontDesc := C.pango_font_description_from_string(C.CString("Sans"))
	C.pango_layout_set_font_description(W.Layout, FontDesc)

	return W, nil
}

var tweetsList []string

func RedrawWindow(W XWindow) {
	var Rect C.PangoRectangle
	yPos := 1000

	C.pango_layout_set_width(W.Layout, C.pango_units_from_double(800.0))

	for i := 0; i < len(tweetsList); i++ {
		t := tweetsList[i]
		C.pango_layout_set_text(W.Layout, C.CString(t), -1)
		C.pango_layout_get_extents(W.Layout, nil, &Rect)
		C.pango_xft_render_layout(W.FontDraw, &W.ColorBlack, W.Layout, 1000, C.int(yPos))
		yPos += 10000 + int(Rect.height)
	}
}

func initDB() (*bolt.DB, error) {
	DB, err := bolt.Open("tweets.db", 0777, nil)
	if err != nil {
		return nil, err
	}
	Tx, err := DB.Begin(true)

	if _, err = Tx.CreateBucketIfNotExists([]byte("tweets")); err != nil {
		return nil, err
	}

	if err := Tx.Commit(); err != nil {
		return nil, err
	}
	return DB, err
}

func main() {

	window, err := CreateXWindow(500, 500)
	if err != nil {
		panic(err)
	}
	DB, err := initDB()
	if err != nil {
		panic(err)
	}
	//getTwitterData(DB)
	tweetsList, err = regenerateViewData(DB, 20)
	if err != nil {
		panic(err)
	}
	//tweetsList = []string{"مرحبا بالعالم", "niño\nmalo", "我的儿子是个女孩"}
	RedrawWindow(window)

	var report C.XEvent
	for {
		C.XNextEvent(window.Display, &report)

		switch C.getXEventType(report) {
		case C.Expose:
			RedrawWindow(window)
			fmt.Println("Exposed!")

		case C.KeyPress:
			ke := C.eventAsKeyEvent(report)
			fmt.Println("Key pressed", ke.keycode)
		}
	}
}

func regenerateViewData(DB *bolt.DB, MaxTweets int) ([]string, error) {
	var Result []string
	Tx, err := DB.Begin(false)
	if err != nil {
		return []string{}, err
	}
	Bucket := Tx.Bucket([]byte("tweets"))
	Cursor := Bucket.Cursor()
	k, v := Cursor.Last()
	for i := 0; i < MaxTweets; i++ {
		if k == nil {
			break
		}
		var tweet anaconda.Tweet
		if err := json.Unmarshal(v, &tweet); err != nil {
			return []string{}, err
		}
		text := tweet.User.Name
		if tweet.RetweetedStatus != nil {
			text += " -Retweeted- "
			text += tweet.RetweetedStatus.User.Name + " -- " + tweet.RetweetedStatus.Text

		} else {
			text += " -- " + tweet.Text
		}
		Result = append(Result, text)
		k, v = Cursor.Prev()
	}
	if err := Tx.Rollback(); err != nil {
		return []string{}, err
	}
	return Result, nil
}

func getTwitterData(DB *bolt.DB) {
	anaconda.SetConsumerKey("KmxA5PMS1WaVdSnJrYtq5XANb")
	anaconda.SetConsumerSecret("yt7ydv2qFt7BpyHrMK3UzIj7HXGGv7ezcVTnELxhgh2WMGj9IA")
	api := anaconda.NewTwitterApi(
		"268263175-deYL6a9YyDMy8YRDQI0p9NDBoKuZScRKG24Dpqkj",
		"PrFnSYOzsZjPYc5zhN9qeviyyHH0x1sKkiOYSSyPdWrnS")

	tweets, err := api.GetHomeTimeline(url.Values{
		"count": {"10"},
	})
	if err != nil {
		// TODO -- Handle timeouts here
		panic(err)
	}

	Tx, err := DB.Begin(true)
	if err != nil {
		// TODO -- Handle this gracely
		panic(err)
	}
	Bucket := Tx.Bucket([]byte("tweets"))
	for _, t := range tweets {

		data, err := json.Marshal(t)
		if err != nil {
			Tx.Rollback()
			DB.Sync()
			panic(err)
		}
		key := []byte(strconv.FormatInt(t.Id, 16))
		if err = Bucket.Put(key, data); err != nil {
			Tx.Rollback()
			DB.Sync()
			panic(err)
		}
	}
	Tx.Commit()
}
