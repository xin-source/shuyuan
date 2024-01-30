package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// warp 请求 source url 的超时
	ConnectTimeout = time.Second * 10
	// 用 hectorqin/reader 验证的超时
	VerifyTimeout  = time.Second * 60
	ConnectThreads = 32
	VerifyThreads  = 8
	Hectorqin      = "http://127.0.0.1:8080"
	SearchBook     = "斗罗大陆"
	SourceURL      = "https://raw.githubusercontent.com/shidahuilang/shuyuan/shuyuan/book.json"
)

func main() {
	// verify whether warp is on
	{
		res, err := httpGet("https://cloudflare.com/cdn-cgi/trace", ConnectTimeout)
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
		m := map[string]string{
			"url": SourceURL,
		}
		b, _ := json.Marshal(m)
		res, err := httpPostJson(fmt.Sprintf("%s/reader3/saveFromRemoteSource?v=%d", Hectorqin, time.Now().UnixMilli()), string(b), time.Minute*10)
		if err != nil {
			panic(err)
		}

		defer res.Body.Close()
		b, _ = io.ReadAll(res.Body)
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			panic(string(b))
		} else {
			log.Println("saveFromRemoteSource", string(b))
		}
	}

	{
		res, err := http.Get(SourceURL)
		if err != nil {
			panic(err)
		}
		defer res.Body.Close()

		var sources = []map[string]interface{}{}
		err = json.NewDecoder(res.Body).Decode(&sources)
		if err != nil {
			panic(err)
		}

		step2in := make(chan map[string]interface{})
		step3in := make(chan map[string]interface{})
		step3out := make(chan map[string]interface{})

		go step1(sources, step2in)

		var wgStep2 sync.WaitGroup
		for i := 0; i < ConnectThreads; i++ {
			wgStep2.Add(1)
			go func() {
				defer wgStep2.Done()
				step2(step2in, step3in)
			}()
		}
		go func() {
			wgStep2.Wait()
			close(step3in)
		}()

		var wgStep3 sync.WaitGroup
		for i := 0; i < VerifyThreads; i++ {
			wgStep3.Add(1)
			go func() {
				defer wgStep3.Done()
				step3(step3in, step3out)
			}()
		}
		go func() {
			wgStep3.Wait()
			close(step3out)
		}()

		var outputs = []map[string]interface{}{}
		for result := range step3out {
			outputs = append(outputs, result)
			log.Println("verified", readString(result, "bookSourceName"), readString(result, "bookSourceUrl"))
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

func verify(u string) bool {
	m := map[string]string{
		"key":           SearchBook,
		"bookSourceUrl": u,
	}
	b, _ := json.Marshal(m)

	res, err := httpPostJson(fmt.Sprintf("%s/reader3/searchBook?v=%d", Hectorqin, time.Now().UnixMilli()), string(b), VerifyTimeout)
	if err != nil {
		log.Println("verify", "post", err)
		return false
	}

	defer res.Body.Close()
	r, _ := io.ReadAll(res.Body)
	log.Println("verify", string(b), string(r))

	var result struct {
		IsSuccess bool `json:"isSuccess"`
	}
	err = json.Unmarshal(r, &result)
	if err != nil {
		return false
	}

	return result.IsSuccess
}

func step1(sources []map[string]interface{}, step2in chan<- map[string]interface{}) {

	for i, source := range sources {
		if i%100 == 0 {
			log.Println("current", i, "/", len(sources))
		}
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
			strings.Contains(bookSourceName, "听") ||
			strings.Contains(bookSourceName, "草榴") ||
			strings.Contains(bookSourceName, "图片") ||
			strings.Contains(strings.ToUpper(bookSourceName), "R18") ||
			strings.Contains(bookSourceName, "🔞") {
			continue
		}

		step2in <- source

	}

	close(step2in)
}

func step2(step2in <-chan map[string]interface{}, step3in chan<- map[string]interface{}) {
	for task := range step2in {
		bookSourceUrl := readString(task, "bookSourceUrl")
		if is2xx(bookSourceUrl) {
			step3in <- task
		}
	}
}

func step3(step3in <-chan map[string]interface{}, step3out chan<- map[string]interface{}) {
	for task := range step3in {
		bookSourceUrl := readString(task, "bookSourceUrl")
		if verify(bookSourceUrl) {
			step3out <- task
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

func httpGet(u string, timeout time.Duration) (*http.Response, error) {
	c := &http.Client{
		Timeout: timeout,
	}

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36 Edg/114.0.1823.58")

	return c.Do(req)
}

func is2xx(u string) bool {
	res, err := httpGet(u, ConnectTimeout)
	return err == nil && res.StatusCode >= 200 && res.StatusCode < 300
}

func httpPostJson(u string, body string, timeout time.Duration) (*http.Response, error) {
	c := &http.Client{
		Timeout: timeout,
	}

	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36 Edg/114.0.1823.58")

	return c.Do(req)
}
