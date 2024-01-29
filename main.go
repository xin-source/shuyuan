package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

const (
	Timeout = time.Second * 10
	Threads = 32
)

func main() {
	d, err := proxy.SOCKS5("tcp", "127.0.0.1:1080", nil, nil)
	if err != nil {
		panic(err)
	}

	tr := &http.Transport{
		DialContext: d.(proxy.ContextDialer).DialContext,
	}

	// verify whether warp is on
	{
		res, err := httpGet(tr, "https://cloudflare.com/cdn-cgi/trace")
		if err != nil {
			panic(err)
		}

		defer res.Body.Close()
		buf, _ := io.ReadAll(res.Body)
		if !bytes.Contains(buf, []byte("warp=on")) {
			panic(string(buf))
		}
	}

	{
		res, err := http.Get("https://raw.githubusercontent.com/shidahuilang/shuyuan/shuyuan/book.json")
		if err != nil {
			panic(err)
		}
		defer res.Body.Close()

		var sources = []map[string]interface{}{}
		err = json.NewDecoder(res.Body).Decode(&sources)
		if err != nil {
			panic(err)
		}

		var wg sync.WaitGroup
		taskChannel := make(chan map[string]interface{})
		resultChannel := make(chan map[string]interface{})

		go produce(sources, taskChannel)

		for i := 0; i < Threads; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				consume(tr, taskChannel, resultChannel)
			}()
		}

		go func() {
			wg.Wait()
			close(resultChannel)
		}()

		var outputs = []map[string]interface{}{}
		for result := range resultChannel {
			outputs = append(outputs, result)
		}

		if len(outputs) > 0 {
			os.Remove("good.json")
			f, err := os.Create("good.json")
			if err != nil {
				panic(err)
			}
			defer f.Close()
			err = json.NewEncoder(f).Encode(outputs)
			if err != nil {
				panic(err)
			}
		}
	}
}

func produce(sources []map[string]interface{}, ch chan<- map[string]interface{}) {

	for _, source := range sources {
		bookSourceUrl := readString(source, "bookSourceUrl")
		if bookSourceUrl == "" {
			continue
		}
		bookSourceName := readString(source, "bookSourceName")

		if strings.Contains(bookSourceName, "禁") ||
			strings.Contains(bookSourceName, "漫") ||
			strings.Contains(bookSourceName, "成人") ||
			strings.Contains(bookSourceName, "BL") ||
			strings.Contains(bookSourceName, "腐") ||
			strings.Contains(bookSourceName, "甜") ||
			strings.Contains(bookSourceName, "🎧") ||
			strings.Contains(bookSourceName, "音乐") ||
			strings.Contains(bookSourceName, "广播") ||
			strings.Contains(bookSourceName, "FM") ||
			strings.Contains(bookSourceName, "耽") ||
			strings.Contains(bookSourceName, "同人") ||
			strings.Contains(bookSourceName, "听书") ||
			strings.Contains(bookSourceName, "草榴") ||
			strings.Contains(bookSourceName, "图片") ||
			strings.Contains(strings.ToUpper(bookSourceName), "R18") ||
			strings.Contains(bookSourceName, "🔞") {
			continue
		}

		ch <- source

	}

	close(ch)
}

func consume(tr http.RoundTripper, in <-chan map[string]interface{}, out chan<- map[string]interface{}) {
	for task := range in {
		log.Println(readString(task, "bookSourceName"), readString(task, "bookSourceUrl"))
		if is2xx(tr, readString(task, "bookSourceUrl")) {
			out <- task
		}
	}
}

func readString(source map[string]interface{}, key string) string {
	if source == nil {
		return ""
	}
	i, ok := source[key]
	if !ok || i == nil {
		return ""
	}
	s, _ := i.(string)
	return s
}

func httpGet(tr http.RoundTripper, u string) (*http.Response, error) {
	c := &http.Client{
		Transport: tr,
		Timeout:   Timeout,
	}

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36 Edg/114.0.1823.58")

	return c.Do(req)
}

func is2xx(tr http.RoundTripper, u string) bool {
	res, err := httpGet(tr, u)
	return err == nil && res.StatusCode >= 200 && res.StatusCode < 300
}
