package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

type title_and_hash struct {
	title string
	hash  string
}

func LibGenDownload(md5_hash_list []title_and_hash) error {
	for _, title_and_hash := range md5_hash_list {

		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return nil
			},
		}

		get_request_url := "https://cdn3.booksdl.org/get.php?" + title_and_hash.hash

		resp, err := client.Get(get_request_url)

		if err != nil {
			log.Fatal(err)
		}
		defer resp.Body.Close()

		fmt.Println(resp)

		fmt.Println(resp.Request.URL.String())

		filename := title_and_hash.title + ".pdf"
		file, err := os.Create(filename)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(file, resp.Body)
		if err != nil {
			return err
		}
	}
	return nil
}
