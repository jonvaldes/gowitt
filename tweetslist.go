package main

/*
#cgo pkg-config: pangocairo
#cgo LDFLAGS: -lX11
#include <pango/pango.h>
#include <pango/pangocairo.h>
#include <cairo/cairo.h>
*/
import "C"

import (
	"fmt"
	"github.com/ChimeraCoder/anaconda"
	"html"
	"strings"
)

func Assert(b bool) {
	if !b {
		panic("Assertion failed")
	}
}

var layoutsCache struct {
	Layouts []*C.PangoLayout
	Cairo   *C.cairo_t
}

func InitLayoutsCache(cairo *C.cairo_t) {
	layoutsCache.Cairo = cairo
}

func getLayout() *C.PangoLayout {
	if len(layoutsCache.Layouts) == 0 {
		return C.pango_cairo_create_layout(layoutsCache.Cairo)
	}
	result := layoutsCache.Layouts[len(layoutsCache.Layouts)-1]
	layoutsCache.Layouts = layoutsCache.Layouts[:len(layoutsCache.Layouts)-1]
	return result
}

func recycleLayout(l *C.PangoLayout) {
	layoutsCache.Layouts = append(layoutsCache.Layouts, l)
}

type TweetInfo struct {
	ID        int64
	Text      string
	UserImage string
	Older     *TweetInfo
	Newer     *TweetInfo
	Layout    *C.PangoLayout
}

func GenerateTweetInfo(W *XWindow, t *anaconda.Tweet) *TweetInfo {
	var text string
	if t.RetweetedStatus != nil {
		text = fmt.Sprintf("<i><small>%s</small></i> <span color='#5C5'>⇄</span> <b>%s</b> <small>@%s</small>\n%s", html.EscapeString(t.User.Name),
			t.RetweetedStatus.User.Name, t.RetweetedStatus.User.ScreenName,
			html.EscapeString(t.RetweetedStatus.Text))

	} else {
		text = fmt.Sprintf("<b>%s</b> <small>@%s</small>\n%s",
			html.EscapeString(t.User.Name),
			t.User.ScreenName,
			html.EscapeString(t.Text))
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
		favoriteColor = "#D22"
	}
	text += fmt.Sprintf("<span color='%s'>❤</span><span size='medium'>%s</span>", favoriteColor, favoriteText)

	// Add RT icon
	retweetColor := "#777"
	retweetText := "      "
	if t.Retweeted {
		retweetColor = "#3D3"
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

	errorText := "[[INTERNAL ERROR, COULD NOT PROCESS TWEET]]"

	var strippedText *C.char = nil //&outputText[0]

	// Generate tweet layout
	if C.pango_parse_markup(C.CString(text), -1, 0,
		&W.AttrList,
		&strippedText, nil, nil) != 1 {
		fmt.Println("error parsing", text)
		strippedText = C.CString(errorText)
	}

	layout := getLayout()
	C.pango_layout_set_font_description(layout, W.FontDesc)
	C.pango_layout_set_attributes(layout, W.AttrList)
	C.pango_layout_set_text(layout, strippedText, -1)

	Result := TweetInfo{
		ID:        t.Id,
		Text:      t.Text,
		UserImage: userImageUrl,
		Layout:    layout,
	}

	return &Result
}

func DestroyTweetInfo(t *TweetInfo) {
	recycleLayout(t.Layout)
	*t = TweetInfo{}
}

type TweetsBuffer struct {
	MaxTweets   int
	CenterTweet *TweetInfo
	Oldest      *TweetInfo
	Newest      *TweetInfo
	NewerCnt    int
	OlderCnt    int
}

func AddNewer(b *TweetsBuffer, t TweetInfo) {
	Assert(t.ID > b.Newest.ID)

	t.Older = b.Newest
	b.Newest.Newer = &t
	b.Newest = &t
	b.NewerCnt++
	if b.NewerCnt > b.MaxTweets {
		// Evict oldest
		oldest := b.Oldest
		b.Oldest = b.Oldest.Newer
		b.Oldest.Older = nil
		b.OlderCnt--
		Assert(b.OlderCnt > 0)
		DestroyTweetInfo(oldest)
	}
}

func AddOlder(b *TweetsBuffer, t TweetInfo) {
	Assert(t.ID < b.Oldest.ID)

	t.Newer = b.Oldest
	b.Oldest.Older = &t
	b.Oldest = &t
	b.OlderCnt++
	if b.OlderCnt > b.MaxTweets {
		// Evict newest
		newest := b.Newest
		b.Newest = b.Newest.Older
		b.Newest.Newer = nil
		b.NewerCnt--
		Assert(b.NewerCnt > 0)
		DestroyTweetInfo(newest)
	}
}

func MoveCenterTweet(b *TweetsBuffer, positions int) {
	for positions > 0 {
		b.CenterTweet = b.CenterTweet.Newer
		positions--
		Assert(b.CenterTweet != nil)
	}
	for positions < 0 {
		b.CenterTweet = b.CenterTweet.Older
		positions++
		Assert(b.CenterTweet != nil)
	}
}
