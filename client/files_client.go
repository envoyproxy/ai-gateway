package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"io"
	"net"
	"net/http"
	"os"
	"time"

	openai "github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/tidwall/gjson"

	"github.com/openai/openai-go/v2/packages/param"
	"github.com/openai/openai-go/v2/shared/constant"
)

func main() {
	baseUrl := os.Getenv("OPENAI_BASE_URL")
	if baseUrl == "" {
		baseUrl = "http://localhost:1975/v1"
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,

		DialContext: (&net.Dialer{
			Timeout:   0,
			KeepAlive: 30 * time.Second,
		}).DialContext,

		TLSHandshakeTimeout:   0,
		ResponseHeaderTimeout: 0,
		ExpectContinueTimeout: 0,
		IdleConnTimeout:       0,
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   0,
	}

	ctx := context.Background()

	client := openai.NewClient(
		option.WithAPIKey("sk-local"), // dummy key for local simulator
		option.WithBaseURL(baseUrl),
		option.WithHTTPClient(httpClient),
		option.WithHeaderAdd("x-envoy-upstream-rq-timeout-ms", "3600000"), // 1 hour
	)

	content := []byte(`{"custom_id": "request-1", "method": "POST", "url": "/v1/chat/completions", "body": {"model": "openai/gpt-oss-20b", "messages": [{"role": "system", "content": "You are a helpful assistant."},{"role": "user", "content": "Hello world!"}],"max_tokens": 1000}}\n
{"custom_id": "request-2", "method": "POST", "url": "/v1/chat/completions", "body": {"model": "openai/gpt-oss-20b", "messages": [{"role": "system", "content": "You are a helpful assistant."},{"role": "user", "content": "Hello world!"}],"max_tokens": 1000}}`)
	fileUploadRequest := openai.FileNewParams{
		File:    bytes.NewReader(content),
		Purpose: openai.FilePurposeBatch,
		ExpiresAfter: openai.FileNewParamsExpiresAfter{
			Seconds: 2592000, // 30 days
			Anchor:  constant.CreatedAt("created_at"),
		},
	}
	fileUploadRequest.SetExtraFields(map[string]any{"model": "openai/gpt-oss-20b"})
	file, err := client.Files.New(
		ctx,
		fileUploadRequest,
	)
	if err != nil {
		panic(err)
	}
	dataByte, err := json.MarshalIndent(file, "", " ")
	if err != nil {
		panic(err)
	}
	fmt.Println("Upload response:", string(dataByte))

	// -------------------------
	// List Files
	// -------------------------
	var after string
	var files []openai.FileObject
	for {
		params := openai.FileListParams{Limit: param.NewOpt[int64](10), Order: "asc"}
		if after != "" {
			params.After = param.NewOpt(after)
		}
		page, err := client.Files.List(ctx, params)
		if err != nil {
			panic(err)
		}
		files = append(files, page.Data...)

		raw := page.RawJSON()
		if !gjson.Get(raw, "has_more").Bool() {
			break
		}
		after = gjson.Get(raw, "last_id").String() // the gateway's flcur- walk cursor
	}

	fmt.Println("\nFiles:")
	for _, f := range files {
		fmt.Println("-", f.ID, f.Filename)
	}

	dataByte, err = json.MarshalIndent(files, "", " ")
	if err != nil {
		panic(err)
	}
	fmt.Println("List response:", string(dataByte))

	// -------------------------
	// Retrieve File
	// -------------------------
	id := file.ID
	retrieved, err := client.Files.Get(ctx, id)
	if err != nil {
		panic(err)
	}
	data, err := json.MarshalIndent(retrieved, "", " ")
	if err != nil {
		panic(err)
	}
	fmt.Println("Retrieve response:", string(data))

	// -------------------------
	// Retrieve File Content
	// -------------------------

	contentResp, err := client.Files.Content(
		ctx,
		id,
	)
	if err != nil {
		panic(err)
	}
	defer contentResp.Body.Close()

	out, err := os.Create("downloaded.jsonl")
	if err != nil {
		panic(err)
	}
	defer out.Close()

	_, err = io.Copy(out, contentResp.Body)
	if err != nil {
		panic(err)
	}

	fmt.Println("\nFile content saved to downloaded.jsonl")

	// -------------------------
	// Delete File
	// -------------------------
	del, err := client.Files.Delete(ctx, id)
	if err != nil {
		panic(err)
	}

	fmt.Println("\nDeleted:", del.Deleted)

	data, err = json.MarshalIndent(del, "", " ")
	if err != nil {
		panic(err)
	}
	fmt.Println("Delete response:", string(data))

	os.Exit(0)
}
