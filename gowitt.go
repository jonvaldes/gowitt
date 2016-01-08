package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ChimeraCoder/anaconda"
	"github.com/boltdb/bolt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

/*
#cgo pkg-config: pangocairo
#cgo LDFLAGS: -lX11
#include <pango/pango.h>
#include <pango/pangocairo.h>
#include <cairo/cairo.h>
#include <cairo/cairo-xlib.h>
int getXEventType(XEvent e){ return e.type; }
XKeyEvent eventAsKeyEvent(XEvent e){ return e.xkey; }
XButtonEvent eventAsButtonEvent(XEvent e){ return e.xbutton; }
long clientMessageType(XEvent e) { return e.xclient.data.l[0]; }
*/
import "C"

const UserImageSize = 48
const UIPadding = 5    // pixels of padding around stuff
const SmallPadding = 2 // pixels of smaller types of padding

type XWindow struct {
	Display *C.Display
	Window  C.Window
	// -- Pango
	PangoContext *C.PangoContext
	Layout       *C.PangoLayout
	AttrList     *C.PangoAttrList
	// Cairo
	Cairo   *C.cairo_t
	Surface *C.cairo_surface_t
	//
	Scroll float64
}

func CreateXWindow(width, height int) (XWindow, error) {
	var W XWindow

	W.Display = C.XOpenDisplay(nil)
	if W.Display == nil {
		return XWindow{}, errors.New("Can't open display")
	}
	W.Window = C.XCreateSimpleWindow(W.Display, C.XDefaultRootWindow(W.Display), 0, 0, C.uint(width), C.uint(height), 0, 0, 0xFF151515)
	C.XSetWindowBackgroundPixmap(W.Display, W.Window, 0) // This avoids flickering on resize
	C.XMapWindow(W.Display, W.Window)
	C.XStoreName(W.Display, W.Window, C.CString("gowitt"))

	C.XSelectInput(W.Display, W.Window, C.ExposureMask|C.KeyPressMask|C.ButtonPressMask)
	C.XFlush(W.Display)

	// Cairo
	W.Surface = C.cairo_xlib_surface_create(W.Display, C.Drawable(W.Window), C.XDefaultVisual(W.Display, 0), C.int(width), C.int(height))
	C.cairo_xlib_surface_set_size(W.Surface, C.int(width), C.int(height))
	W.Cairo = C.cairo_create(W.Surface)

	// Pango
	W.PangoContext = C.pango_cairo_create_context(W.Cairo)
	W.Layout = C.pango_cairo_create_layout(W.Cairo)
	FontDesc := C.pango_font_description_from_string(C.CString("Sans 10"))
	C.pango_layout_set_font_description(W.Layout, FontDesc)

	W.AttrList = C.pango_attr_list_new()

	testImage = C.cairo_image_surface_create_from_png(C.CString("test.png"))
	//fmt.Println(C.GoString(C.cairo_status_to_string(C.cairo_surface_status(testImage))))
	return W, nil
}

var testImage *C.cairo_surface_t

func PixelsToPango(u float64) C.int {
	return C.pango_units_from_double(C.double(u))
}

func PangoToPixels(u C.int) float64 {
	return float64(C.pango_units_to_double(u))
}

func PangoRectToPixels(P *C.PangoRectangle) (x, y, w, h float64) {
	return float64(C.pango_units_to_double(P.x)),
		float64(C.pango_units_to_double(P.y)),
		float64(C.pango_units_to_double(P.width)),
		float64(C.pango_units_to_double(P.height))
}

func RedrawWindow(W *XWindow, tweetsList []string) {

	var Attribs C.XWindowAttributes
	C.XGetWindowAttributes(W.Display, W.Window, &Attribs)
	// TODO -- Do this only when resizing?
	C.cairo_xlib_surface_set_size(W.Surface, Attribs.width, Attribs.height)

	C.cairo_set_source_rgb(W.Cairo, 0.1, 0.1, 0.1)
	C.cairo_paint(W.Cairo)

	var Rect C.PangoRectangle
	yPos := 10.0 + W.Scroll

	WindowWidth := Attribs.width
	C.pango_layout_set_width(W.Layout, PixelsToPango(float64(WindowWidth-3*UIPadding-UserImageSize)))

	ParsedText := "                                                                                                                                                                                                                "

	var strptr *C.char = C.CString(ParsedText)

	for i := 0; i < len(tweetsList); i++ {
		t := tweetsList[i]

		// Generate tweet layout
		C.pango_parse_markup(C.CString(t), -1, 0,
			&W.AttrList,
			&strptr, nil, nil)

		C.pango_layout_set_attributes(W.Layout, W.AttrList)
		C.pango_layout_set_text(W.Layout, strptr, -1)
		C.pango_layout_get_extents(W.Layout, nil, &Rect)

		// Get tweet text size
		_, ry, _, rh := PangoRectToPixels(&Rect)

		// Position and add padding
		ry += yPos
		if rh < UserImageSize+2*UIPadding-UIPadding {
			rh = UserImageSize + 2*UIPadding
		} else {
			rh += UIPadding
		}

		// Draw rectangle around tweet
		C.cairo_set_source_rgb(W.Cairo, 0.2, 0.2, 0.2)
		C.cairo_rectangle(W.Cairo, UIPadding, C.double(ry), C.double(WindowWidth-2*UIPadding), C.double(rh))
		C.cairo_fill(W.Cairo)

		// Draw user image
		C.cairo_set_source_surface(W.Cairo, testImage, 2*UIPadding, C.double(yPos+UIPadding))
		C.cairo_paint(W.Cairo)

		// Draw tweet text
		C.cairo_move_to(W.Cairo, 63, C.double(yPos+SmallPadding))
		C.cairo_set_source_rgb(W.Cairo, 0.95, 0.95, 0.95)
		C.pango_cairo_show_layout(W.Cairo, W.Layout)
		yPos += 5 + rh
	}

}

func main() {

	window, err := CreateXWindow(500, 500)
	if err != nil {
		panic(err)
	}

	defer C.XCloseDisplay(window.Display)

	DB, err := initDB()
	if err != nil {
		panic(err)
	}

	//getTwitterData(DB)
	tweetsList, err := regenerateViewData(DB, 20)
	if err != nil {
		panic(err)
	}

	wmDeleteMessage := C.XInternAtom(window.Display, C.CString("WM_DELETE_WINDOW"), 0)
	C.XSetWMProtocols(window.Display, window.Window, &wmDeleteMessage, 1)
	var event C.XEvent
	for {
		pendingRedraws := false
		for C.XPending(window.Display) != 0 {
			C.XNextEvent(window.Display, &event)

			switch C.getXEventType(event) {
			case C.Expose:
				pendingRedraws = true
			case C.KeyPress:
				ke := C.eventAsKeyEvent(event)
				//fmt.Println("Key pressed", ke.keycode)
				switch ke.keycode {
				case 116: // down
					window.Scroll -= 10
				case 111: // up
					window.Scroll += 10
				}
				pendingRedraws = true
			case C.ButtonPress:
				b := C.eventAsButtonEvent(event)
				switch b.button {
				case 4: // scroll up
					window.Scroll += 10
				case 5: // scroll down
					window.Scroll -= 10

				}
				pendingRedraws = true
			case C.ClientMessage:
				if C.clientMessageType(event) == C.long(wmDeleteMessage) {
					return
				}
			}
		}
		if pendingRedraws {
			RedrawWindow(&window, tweetsList)
		}
	}
}

func regenerateViewData(DB *bolt.DB, MaxTweets int) ([]string, error) {
	tweets, err := getLastNTweets(DB, MaxTweets)
	if err != nil {
		return []string{}, err
	}
	var Result []string

	for _, t := range tweets {
		var text string
		if t.RetweetedStatus != nil {
			text = "<i><small>" + html.UnescapeString(t.User.Name) + "</small></i> <span color='#5C5'>⇄</span> <b>" +
				t.RetweetedStatus.User.Name + "</b> <small>@" + t.RetweetedStatus.User.ScreenName + "</small>\n" +
				html.UnescapeString(t.RetweetedStatus.Text)

		} else {
			text = "<b>" + html.UnescapeString(t.User.Name) + "</b> <small>@" + t.User.ScreenName + "</small>\n" + html.UnescapeString(t.Text)
		}
		text = replaceURLS(text, func(s string) string { return "<span color='#55E'>" + s + "</span>" })
		Result = append(Result, text)
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

		tweetText := t.Text
		userImageUrl := t.User.ProfileImageURL
		if t.RetweetedStatus != nil {
			tweetText = t.RetweetedStatus.Text
			userImageUrl = t.RetweetedStatus.User.ProfileImageURL
		}
		fmt.Println(userImageUrl)
		tweetText = replaceURLS(tweetText, func(s string) string {
			fmt.Println("Replacing ", s)
			for retries := 0; retries < 3; retries++ {
				newS, err := getRedirectedURL(s)
				if err != nil {
					time.Sleep(time.Duration(1+retries) * time.Second)
					continue
				}
				return newS
			}
			return s
		})
		if t.RetweetedStatus != nil {
			t.RetweetedStatus.Text = tweetText
		} else {
			t.Text = tweetText
		}
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

// Auxiliary function to get original URLs from URL shorteners
func getRedirectedURL(URL string) (string, error) {

	var Result string
	c := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			Result = req.URL.String()
			return errors.New("")
		}}

	_, err := c.Get(URL)

	if Result != "" {
		return Result, nil
	}

	return Result, err
}

func replaceURLS(s string, txFunc func(string) string) string {

	var output string
	for {
		// Find instances of http(s)://
		p := strings.Index(s, "http://")
		if p == -1 {
			p = strings.Index(s, "https://")
		}

		// Add non-url text to output string
		if p == -1 {
			output += s
			break
		}

		if p > 0 {
			output += s[:p]
			s = s[p:]
		}

		// Find where url ends (space or string end)
		end := strings.Index(s, " ")
		if end == -1 {
			end = len(s)
		}

		// transform url
		newUrl := txFunc(s[:end])
		output += newUrl
		s = s[end:]
	}

	return output
}
