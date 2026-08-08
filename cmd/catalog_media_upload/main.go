package main

import (
	"flag"
	"fmt"
	"log"
	"path"

	ecommerceupload "ecommerce-media-multipart"
)

func main() {
	var bucket, fileName, objectKey, contentType string
	var partMB int
	flag.StringVar(&bucket, "bucket", "catalog-media", "destination bucket")
	flag.StringVar(&fileName, "file", "", "local product media file")
	flag.StringVar(&objectKey, "key", "", "object key, such as products/SKU-42/video.mp4")
	flag.StringVar(&contentType, "content-type", "video/mp4", "media MIME type")
	flag.IntVar(&partMB, "part-mb", 8, "part size in MiB")
	flag.Parse()

	if fileName == "" {
		log.Fatal("-file is required")
	}
	if objectKey == "" {
		objectKey = path.Join("products", path.Base(fileName))
	}
	if partMB < 1 {
		log.Fatal("-part-mb must be at least 1")
	}

	client, err := ecommerceupload.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	if err := client.UploadFile(bucket, objectKey, fileName, contentType, int64(partMB)*1024*1024); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("uploaded %s to %s/%s\n", fileName, bucket, objectKey)
}
