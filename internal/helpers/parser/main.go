package parser

import (
	"bytes"
	"github.com/PuerkitoBio/goquery"
	"os"
)

func ParsePageData(pageData []byte) goquery.Document {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(pageData))
	if err != nil {
		panic(err)
	}

	return *doc
}

func GetNextPageUrl(doc *goquery.Document) string {
	var foundCurrent bool
	var nextPage string

	doc.Find("nav.paginator > ul > li").Each(func(i int, selection *goquery.Selection) {
		if !foundCurrent && selection.HasClass("current") {
			foundCurrent = true
		} else if foundCurrent {
			url, _ := selection.Children().First().Attr("href")
			nextPage = AddHostToUri(url)
		}
	})

	return nextPage
}

func AddHostToUri(uri string) string {
	return os.Getenv("SOURCE_BASE_URL") + uri
}
