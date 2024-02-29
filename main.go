package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

var config struct {
	SourceURL               string   `yaml:"source_url"`
	UserAgent               string   `yaml:"user_agent"`
	Excludes                []string `yaml:"excludes"`
	ExcludesInsensitiveCase []string `yaml:"excludes_insensitive_case"`
	ConnectThreads          int      `yaml:"connect_treads"`
	// warp 请求 source url 的超时
	ConnectTimeout int `yaml:"connect_timeout"`
	VerifyThreads  int `yaml:"verify_threads"`
	// 用 hectorqin/reader 验证的超时
	VerifyTimeout int `yaml:"verify_timeout"`
	VerifyBooks   []struct {
		Name   string `yaml:"name"`
		Author string `yaml:"author"`
	} `yaml:"verify_books"`
	VerifyAPI      string `yaml:"verify_api"`
	ConnectViaWarp bool   `yaml:"connect_via_warp"`
}

func main() {
	configFile := flag.String("c", "config.yaml", "")
	flag.Parse()

	{
		b, err := os.ReadFile(*configFile)
		if err != nil {
			panic(err)
		}

		err = yaml.Unmarshal(b, &config)
		if err != nil {
			panic(err)
		}

		b, _ = yaml.Marshal(config)

		if config.SourceURL == "" ||
			config.ConnectThreads <= 0 || config.ConnectTimeout <= 0 ||
			config.VerifyThreads <= 0 || config.VerifyTimeout <= 0 ||
			len(config.VerifyBooks) == 0 || config.VerifyBooks[0].Name == "" || config.VerifyBooks[0].Author == "" ||
			config.VerifyAPI == "" {
			panic(string(b))
		}

		log.Println("config", "\n"+string(b))
	}

	if config.ConnectViaWarp {
		res, err := httpGet("https://cloudflare.com/cdn-cgi/trace", time.Second*time.Duration(config.ConnectTimeout))
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
		err := saveFromRemoteSource(5)
		if err != nil {
			panic(err)
		}
	}

	{
		res, err := http.Get(config.SourceURL)
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
		for i := 0; i < config.ConnectThreads; i++ {
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
		for i := 0; i < config.VerifyThreads; i++ {
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

func saveFromRemoteSource(retry uint8) error {
	i := uint8(0)

	var err error

	for i <= retry {
		i++
		m := map[string]string{
			"url": config.SourceURL,
		}
		b, _ := json.Marshal(m)
		var res *http.Response
		res, err = httpPostJson(fmt.Sprintf("%s/reader3/saveFromRemoteSource?v=%d", config.VerifyAPI, time.Now().UnixMilli()), string(b), time.Minute*10)
		if err != nil {
			log.Println("saveFromRemoteSource", err)
			continue
		}

		defer res.Body.Close()
		b, _ = io.ReadAll(res.Body)
		log.Println("saveFromRemoteSource", string(b))

		var result struct {
			IsSuccess bool `json:"isSuccess"`
		}
		err = json.Unmarshal(b, &result)
		if err != nil {
			log.Println("saveFromRemoteSource", err)
			continue
		}

		if !result.IsSuccess {
			err = errors.New(string(b))
			log.Println("saveFromRemoteSource", err)
			continue
		}

		return nil
	}

	return err
}

func step1(sources []map[string]interface{}, step2in chan<- map[string]interface{}) {

	m := map[string]byte{}
	for i, source := range sources {
		if i%100 == 0 {
			log.Println("current", i, "/", len(sources))
		}
		bookSourceUrl := readString(source, "bookSourceUrl")
		if bookSourceUrl == "" {
			continue
		}
		_, ok := m[bookSourceUrl]
		if ok {
			continue
		}
		m[bookSourceUrl] = 1

		bookSourceName := readString(source, "bookSourceName")

		if shouldExclude(bookSourceName) {
			continue
		}

		exploreUrl := readString(source, "exploreUrl")
		if exploreUrl != "" && shouldExclude(exploreUrl) {
			continue
		}

		bookSourceGroup := readString(source, "bookSourceGroup")
		if bookSourceGroup != "" && shouldExclude(bookSourceGroup) {
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

	req.Header.Set("user-agent", config.UserAgent)

	return c.Do(req)
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
	req.Header.Set("user-agent", config.UserAgent)

	return c.Do(req)
}

func shouldExclude(name string) bool {
	if strings.TrimSpace(name) == "" {
		return true
	}

	for _, e := range config.Excludes {
		if strings.Contains(name, e) {
			return true
		}
	}

	for _, e := range config.ExcludesInsensitiveCase {
		if strings.Contains(strings.ToLower(name), strings.ToLower(e)) {
			return true
		}
	}

	return false
}

func is2xx(u string) bool {
	res, err := httpGet(u, time.Second*time.Duration(config.ConnectTimeout))
	return err == nil && res.StatusCode >= 200 && res.StatusCode < 300
}

func verify(u string) bool {
	for i := range config.VerifyBooks {
		if verifySingle(u, config.VerifyBooks[i].Name, config.VerifyBooks[i].Author) {
			return true
		}
	}
	return false
}

func truncate(b []byte) []byte {
	max := 64
	if len(b) > max {
		return b[:max]
	} else {
		return b
	}
}

func verifySingle(u, bookName, bookAuthor string) bool {
	m := map[string]string{
		"key":           bookName,
		"bookSourceUrl": u,
	}
	b, _ := json.Marshal(m)

	res, err := httpPostJson(fmt.Sprintf("%s/reader3/searchBook?v=%d", config.VerifyAPI, time.Now().UnixMilli()), string(b), time.Second*time.Duration(config.VerifyTimeout))
	if err != nil {
		log.Println("verify", "post", err)
		return false
	}

	defer res.Body.Close()
	r, _ := io.ReadAll(res.Body)
	log.Println("verify", string(b), string(truncate(r)))

	var result struct {
		IsSuccess bool                     `json:"isSuccess"`
		Data      []map[string]interface{} `json:"data"`
	}
	err = json.Unmarshal(r, &result)
	if err != nil {
		return false
	}

	if !result.IsSuccess {
		return false
	}

	found := false
	for _, data := range result.Data {
		author, ok := data["author"]
		if !ok {
			continue
		}

		if author == nil {
			continue
		}

		authorStr, _ := author.(string)
		if authorStr == bookAuthor {
			found = true
			break
		}
	}

	return found
}
