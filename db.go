package main

import (
	"encoding/json"
	"github.com/ChimeraCoder/anaconda"
	"github.com/boltdb/bolt"
)

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

func getLastNTweets(DB *bolt.DB, TweetCnt int) ([]anaconda.Tweet, error) {
	var Result []anaconda.Tweet
	Tx, err := DB.Begin(false)
	if err != nil {
		return []anaconda.Tweet{}, err
	}
	Bucket := Tx.Bucket([]byte("tweets"))
	Cursor := Bucket.Cursor()
	k, v := Cursor.Last()
	for i := 0; i < TweetCnt; i++ {
		if k == nil {
			break
		}
		var tweet anaconda.Tweet
		if err := json.Unmarshal(v, &tweet); err != nil {
			return []anaconda.Tweet{}, err
		}
		Result = append(Result, tweet)
		k, v = Cursor.Prev()
	}
	if err := Tx.Rollback(); err != nil {
		return []anaconda.Tweet{}, err
	}
	return Result, nil
}
