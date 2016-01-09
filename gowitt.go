package main

/*
TODO:
	- Implement correct scrolling
	- Display new tweets before replacing shortened urls, then expand urls as they arrive
	- Stream tweets from the DB when scrolling up or down
	- Asynchronous twitter timeline updating
	- Do UI interaction (IMGUI-style maybe?)
	- The image cache doesn't yet evict old images when new ones come in
	- Add tweet time
	- Proper error-handling everywhere

Known issues:
	- The UI freezes when images are downloaded in the background, even though system is
	architected so it's should never block. Which I guess means it's blocking somewhere
	inside X11...
	- The image cache doesn't yet evict old images when new ones come in
	- Images are never removed from the cache directory
	- Cairo surface is resized on every redraw. I think it should only do that upon
	window resize

*/

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
	"unsafe"
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
	Scroll     float64
	UserImages *ImageCache
}

func CreateXWindow(width, height int) (*XWindow, error) {
	C.XInitThreads()

	W := &XWindow{}

	W.Display = C.XOpenDisplay(nil)
	if W.Display == nil {
		return &XWindow{}, errors.New("Can't open display")
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

	placeholderImage = C.cairo_image_surface_create_from_png(C.CString("test.png"))

	W.UserImages = NewImageCache(func() {
		var ev C.XEvent
		exev := (*C.XExposeEvent)(unsafe.Pointer(&ev))
		exev._type = C.Expose
		exev.count = 0
		exev.window = W.Window
		exev.send_event = 1
		exev.display = W.Display

		C.XSendEvent(W.Display, W.Window, 0, C.ExposureMask, &ev)
		C.XFlush(W.Display)
	})
	return W, nil
}

var placeholderImage *C.cairo_surface_t

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

type TweetInfo struct {
	Text      string
	UserImage string
}

func RedrawWindow(W *XWindow, tweetsList []TweetInfo, mouseClick [2]int) {
	var Attribs C.XWindowAttributes
	C.XGetWindowAttributes(W.Display, W.Window, &Attribs)
	// TODO -- Do this only when resizing?
	C.cairo_xlib_surface_set_size(W.Surface, Attribs.width, Attribs.height)

	C.cairo_set_source_rgb(W.Cairo, 0.1, 0.1, 0.1)
	C.cairo_paint(W.Cairo)

	var Rect C.PangoRectangle
	yPos := 10.0 + W.Scroll

	WindowWidth := Attribs.width
	C.pango_layout_set_width(W.Layout, PixelsToPango(float64(WindowWidth-5*UIPadding-UserImageSize)))

	errorText := "[[INTERNAL ERROR, COULD NOT PROCESS TWEET]]"

	for i := 0; i < len(tweetsList); i++ {
		t := tweetsList[i]

		var strippedText *C.char = nil //&outputText[0]
		// Generate tweet layout
		if C.pango_parse_markup(C.CString(t.Text), -1, 0,
			&W.AttrList,
			&strippedText, nil, nil) != 1 {
			fmt.Println("error parsing", t.Text)
			strippedText = C.CString(errorText)
		}

		C.pango_layout_set_attributes(W.Layout, W.AttrList)
		C.pango_layout_set_text(W.Layout, strippedText, -1)
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
		if mouseClick[0] >= UIPadding && float64(mouseClick[1]) >= ry && float64(mouseClick[1]) <= ry+rh {
			fmt.Println("Clicked tweet", t.Text)
		}

		// Draw user image
		userImage := GetCachedImage(W.UserImages, t.UserImage)
		if userImage == nil || C.cairo_surface_status(userImage) != C.CAIRO_STATUS_SUCCESS {
			userImage = placeholderImage
		}
		C.cairo_set_source_surface(W.Cairo, userImage, 2*UIPadding, C.double(yPos+UIPadding))
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

	getTwitterData(DB)
	tweetsList, err := regenerateViewData(window, DB, 20)
	if err != nil {
		panic(err)
	}

	wmDeleteMessage := C.XInternAtom(window.Display, C.CString("WM_DELETE_WINDOW"), 0)
	C.XSetWMProtocols(window.Display, window.Window, &wmDeleteMessage, 1)
	mouseClick := [2]int{-1, -1}
	var event C.XEvent
	for {
		pendingRedraws := false
		processedOneEvent := false
		for !processedOneEvent || C.XPending(window.Display) != 0 {
			C.XNextEvent(window.Display, &event)
			processedOneEvent = true

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
				case 1:
					// left mouse down
					butEv := (*C.XButtonEvent)(unsafe.Pointer(&event))
					mouseClick[0] = int(butEv.x)
					mouseClick[1] = int(butEv.y)
				}
				pendingRedraws = true
			case C.ClientMessage:
				if C.clientMessageType(event) == C.long(wmDeleteMessage) {
					return
				}
			}
		}
		if pendingRedraws {
			RedrawWindow(window, tweetsList, mouseClick)
			mouseClick[0] = -1
			mouseClick[1] = -1
		}
	}
}

func regenerateViewData(W *XWindow, DB *bolt.DB, MaxTweets int) ([]TweetInfo, error) {
	tweets, err := getLastNTweets(DB, MaxTweets)
	if err != nil {
		return []TweetInfo{}, err
	}
	var Result []TweetInfo

	for _, t := range tweets {
		var text string
		if t.RetweetedStatus != nil {
			text = "<i><small>" + html.EscapeString(t.User.Name) + "</small></i> <span color='#5C5'>⇄</span> <b>" +
				t.RetweetedStatus.User.Name + "</b> <small>@" + t.RetweetedStatus.User.ScreenName + "</small>\n" +
				html.EscapeString(t.RetweetedStatus.Text)

		} else {
			text = "<b>" + html.EscapeString(t.User.Name) + "</b> <small>@" + t.User.ScreenName + "</small>\n" + html.EscapeString(t.Text)
		}
		text = strings.Replace(text, "&amp;", "&", -1)
		text = replaceURLS(text, func(s string) string { return "<span color='#88F'>" + s + "</span>" })
		text += "\n<span size='x-large' color='#777'>↶     "

		// Add favorite icon
		favoriteColor := "#777"
		favoriteText := "      "
		favoriteCount := t.FavoriteCount
		if t.RetweetedStatus != nil {
			favoriteCount = t.RetweetedStatus.FavoriteCount
		}
		if favoriteCount > 0 {
			favoriteText = fmt.Sprintf("<span size='medium'> %-4d </span>", favoriteCount)
		}
		if t.Favorited {
			favoriteColor = "#F33"
		}
		text += fmt.Sprintf("<span color='%s'>❤</span><span size='medium'>%s</span>", favoriteColor, favoriteText)

		// Add RT icon
		retweetColor := "#777"
		retweetText := "      "
		if t.Retweeted {
			retweetColor = "#3F3"
		}
		retweetCount := t.RetweetCount
		if t.RetweetedStatus != nil {
			retweetCount = t.RetweetedStatus.RetweetCount
		}
		if retweetCount > 0 {
			retweetText = fmt.Sprintf("<span size='medium'> %-4d </span>", retweetCount)
		}
		text += fmt.Sprintf("<span color='%s'>⇄</span>%s", retweetColor, retweetText)

		// Add "more options" icon
		text += "<span color='#777'>…</span></span>"

		userImageUrl := t.User.ProfileImageURL
		if t.RetweetedStatus != nil {
			userImageUrl = t.RetweetedStatus.User.ProfileImageURL
		}

		Result = append(Result, TweetInfo{text, userImageUrl})
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
		if t.RetweetedStatus != nil {
			tweetText = t.RetweetedStatus.Text
		}
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

// Replace all urls found in input string with the output of the supplied function
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
