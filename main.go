package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	json2 "encoding/json"
	"errors"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/goodsign/monday"
	"github.com/joho/godotenv"
	"github.com/streadway/amqp"
	"io"
	"io/ioutil"
	"itsurka/go-web-parser/internal/dto"
	"itsurka/go-web-parser/internal/helpers/dbhelper"
	eh "itsurka/go-web-parser/internal/helpers/errhelper"
	"itsurka/go-web-parser/internal/helpers/parser"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	//testGoruotines()
	readAmqp()
	//importApartments()
}

func testGoruotines() {
	var waitGroup sync.WaitGroup
	fmt.Printf("%#v/n", waitGroup)

	for i := 1; i <= 3; i++ {
		waitGroup.Add(1)
		go func(x int) {
			defer waitGroup.Done()
			fmt.Printf("#%d ", x)
		}(i)
	}

	fmt.Printf("#%#v\n", waitGroup)
	waitGroup.Wait()
	fmt.Println("\nexiting...")
}

func readAmqp() {
	log.Println("Read messages...")

	conn, err := amqp.Dial("amqp://guest:guest@rabbitmq:5672/")
	eh.FailOnError(err)
	defer conn.Close()

	ch, err := conn.Channel()
	eh.FailOnError(err)

	q, err := ch.QueueDeclare(
		"events",
		false,
		false,
		false,
		false,
		nil,
	)
	eh.FailOnError(err)

	messages, err := ch.Consume(
		q.Name,
		"",
		true,
		false,
		false,
		false,
		nil,
	)
	eh.FailOnError(err)

	forever := make(chan bool)

	go func() {
		for delivery := range messages {
			log.Printf("Received a message: %s\n", delivery.Body)
			handleMessage(delivery)
		}
	}()

	log.Printf(" [*] Waiting for messages. To exit press CTRL+C")
	<-forever
}

type Message struct {
	Version string
	Event   string
	Data    interface{}
}

func handleMessage(delivery amqp.Delivery) {
	var message Message
	err := json2.Unmarshal(delivery.Body, &message)
	eh.FailOnError(err)

	switch message.Version {
	case "1":
		switch message.Event {
		case "api.apartments.import":
			importApartments()
		default:
			log.Panicln("Unknown message event", message)
		}

	default:
		log.Panicln("Unknown message event", message)
	}
}

func importApartments() {
	fmt.Println("Start importing apartments...")
	err := godotenv.Load(".env")

	dbConfig := dbhelper.DbConfig{
		os.Getenv("DB_DRIVER"),
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_NAME"),
	}

	db := dbhelper.GetConnection(dbConfig)

	favoritesPage, err := getPage("https://999.md/cabinet/favorites?&subcategory_url=real-estate/apartments-and-rooms")
	if err != nil {
		panic(err)
	}

	apartmentLinks, unavailableApartmentLinks := getApartmentLinks(favoritesPage)

	setApartmentsAsUnavailable(db, unavailableApartmentLinks)

	for _, link := range apartmentLinks {
		data, err := getPage(link)
		if err != nil {
			panic(err)
		}

		apartment := parseApartment(link, data)
		saveApartment(db, apartment)
	}

	fmt.Println("done, imported items", len(apartmentLinks))
}

func parseApartment(pageUrl string, pageData []byte) dto.Apartment {
	apartment := dto.Apartment{
		URL: pageUrl,
	}

	doc := parser.ParsePageData(pageData)
	apartment.Title = doc.Find("header h1").Text()
	apartment.Desc = doc.Find(".adPage__content__description").Text()
	apartment.Desc = doc.Find(".adPage__content__description").Text()

	prices := doc.Find(".adPage__content .adPage__content__price-feature__prices .adPage__content__price-feature__prices__price__value")

	prices.Each(func(i int, selection *goquery.Selection) {
		switch i {
		case 0:
			contentEur, exists := selection.Attr("content")
			if exists == false {
				panic("Item not found")
			}
			priceEur, err := strconv.Atoi(contentEur)
			if err != nil {
				panic(err)
			}
			apartment.PriceEur = priceEur
		case 1:
			contentUsd, exists := selection.Attr("content")
			if exists == false {
				panic("Item not found")
			}
			priceUsd, err := strconv.Atoi(contentUsd)
			if err != nil {
				panic(err)
			}
			apartment.PriceUsd = priceUsd
		case 2:
			contentLeu, exists := selection.Attr("content")
			if exists == false {
				panic("Item not found")
			}
			priceLeu, err := strconv.Atoi(contentLeu)
			if err != nil {
				panic(err)
			}
			apartment.PriceLeu = priceLeu
		}
	})

	aSide := doc.Find(".adPage__aside__stats").First()

	apartment.SellerLogin = strings.TrimSpace(aSide.Find(".adPage__aside__stats__owner a").First().Text())

	updateDateNode := aSide.Find(".adPage__aside__stats__date").First()
	if updateDateNode.Length() == 1 {
		dateText := updateDateNode.Text()
		updateDatePrefix := "Дата обновления: "
		datePart := dateText[len(updateDatePrefix):]
		datePart = strings.Replace(datePart, "сент.", "сен.", 1)
		loc, err := time.LoadLocation("Europe/Moscow")
		if err != nil {
			panic(err)
		}
		updateDate, updateDateErr := monday.ParseInLocation("2 Jan. 2006, 15:04", datePart, loc, monday.LocaleRuRU)
		if updateDateErr != nil {
			updateDate, updateDateErr = monday.ParseInLocation("2 January 2006, 15:04", datePart, loc, monday.LocaleRuRU)
			if updateDateErr != nil {
				panic(updateDateErr)
			}
		}

		apartment.LastUpdated = updateDate.Format("2006-01-02 15:04:05")
	}

	viewsText := aSide.Find(".adPage__aside__stats__views").First().Text()
	apartment.PageViews = parseViews(viewsText)

	apartment.Location = strings.TrimSpace(aSide.Find(".adPage__aside__address-feature__text").First().Text())

	pricePerSqMeter, _ := aSide.Find(".adPage__content__price-feature__labels__price-per-m__value").First().Attr("content")
	apartment.PriceSquareMeterEur, _ = strconv.Atoi(strings.ReplaceAll(pricePerSqMeter, " ", ""))

	//phoneHtml, _ := aSide.Find(".js-phone-number.adPage__content__phone").First().Html()
	phoneLink := doc.Find(".js-phone-number.adPage__content__phone a").First()
	//fmt.Println(phoneHtml)
	phone, _ := phoneLink.Attr("href")
	apartment.SellerPhone = phone[5:]

	doc.Find(".js-fancybox.mfp-zoom.mfp-image").Each(func(i int, selection *goquery.Selection) {
		imageUrl, _ := selection.Attr("data-mfp-src")
		apartment.ImageUrls = append(apartment.ImageUrls, imageUrl)
	})

	return apartment
}

func setApartmentsAsUnavailable(db *sql.DB, urls []string) {
	if len(urls) == 0 {
		return
	}

	urlStr := []string{}
	for _, value := range urls {
		urlStr = append(urlStr, "'"+value+"'")
	}
	urlStrRow := strings.Join(urlStr, ",")

	_, err := db.Exec("UPDATE apartments SET unavailable_from = CURRENT_TIMESTAMP WHERE url IN (" + urlStrRow + ")")
	if err != nil {
		panic(err)
	}
}

func saveApartment(db *sql.DB, apartment dto.Apartment) {
	//query := "INSERT INTO apartments (url, title, description, price_eur, price_usd, price_leu, price_square_meter_eur, " +
	//	"location, last_updated, page_views, seller_login, seller_phone, image_urls) " +
	//	"VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) " +
	//	"ON CONFLICT (url) " +
	//	"DO UPDATE SET title = $14, description = $15, price_eur = $16, price_usd = $17, price_leu = $18, price_square_meter_eur = $19, " +
	//	"location = $20, last_updated = $21, page_views = $22, seller_login = $23, seller_phone = $24, image_urls = $25, updated_at = CURRENT_TIMESTAMP"

	query := "INSERT INTO apartments (url, title, description, price_eur, price_usd, price_leu, price_square_meter_eur, " +
		"location, last_updated, page_views, seller_login, seller_phone, image_urls) " +
		"VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) "

	stmt, err := db.Prepare(query)
	if err != nil {
		panic(err)
	}

	imageUrls, _ := json.Marshal(apartment.ImageUrls)

	_, err = stmt.Exec(
		apartment.URL,
		apartment.Title,
		apartment.Desc,
		apartment.PriceEur,
		apartment.PriceUsd,
		apartment.PriceLeu,
		apartment.PriceSquareMeterEur,
		apartment.Location,
		apartment.LastUpdated,
		apartment.PageViews,
		apartment.SellerLogin,
		apartment.SellerPhone,
		string(imageUrls),
	)
	if err != nil {
		panic(err)
	}
}

func getApartmentLinks(data []byte) ([]string, []string) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		panic(err)
	}

	favorites := doc.Find("#js-favorites-list .favorites-list__items__item__info")

	var links []string
	var unavailableLinks []string

	favorites.Each(func(i int, item *goquery.Selection) {
		parent := item.Parent()
		parentClass, _ := parent.Attr("class")

		isAvailable := strings.Index(parentClass, "is-unavailable") == -1

		lastBreadcrumb := item.Find(".favorites-list__items__item__info__meta .favorites-list__items__item__info__meta__breadcrumbs a").First().Text()
		apartmentPos := strings.Index(lastBreadcrumb, "Недвижимость")

		if apartmentPos == 0 {
			link := item.Find(".favorites-list__items__item__info__title a.link").First()

			href, exists := link.Attr("href")
			if exists == false {
				panic("href not found")
			}

			fullUrl := addHostToUri(href)
			if isAvailable == true {
				links = append(links, fullUrl)
			} else {
				unavailableLinks = append(unavailableLinks, fullUrl)
			}
		}
	})

	return links, unavailableLinks
}

func addHostToUri(uri string) string {
	return "https://999.md" + uri
}

func getPage(link string) ([]byte, error) {
	httpClient := http.Client{}

	urlObj, err := url.Parse(link)
	if err != nil {
		return nil, err
	}

	reqHeader := http.Header{}
	reqHeader.Add("authority", "999.md")
	reqHeader.Add("accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.9")
	reqHeader.Add("accept-language", "en-GB,en-US;q=0.9,en;q=0.8,ru;q=0.7,bg;q=0.6,ro;q=0.5,fr;q=0.4")
	reqHeader.Add("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/103.0.0.0 Safari/537.36")
	reqHeader.Add("cookie", "utid=\"2|1:0|10:1651567506|4:utid|56:VVRiTTZEUGlSd21DVGxNZnpqeDZFUnp6WW9CSVpFaTRqNFptVW1ZZg==|9eb7ce51d4341459a371807da731e62efec0e19ab22ce87f8446c89703984d08\"; _xsrf=2|d98e0748|78419781ba70fa4144d4fcb7fdbd474f|1651567506; foreign_cookie=1; foo=bar; search_exact_match=no; show_all_checked_childrens=no; transnistria_raduga_popup=yes; hide_duplicates=yes; selected_currency=eur; simpalsid.auth=b449002fab1d5099d52eac71ac75694979fc1cc8c5613d838dc017c9bf7294288540a3fdf23688432fffae897f362fc4217c23fbe86d6e333f8ba406916dbad537e0ce1da27867f05fcf49750da276e6a28cf9a1572dc1fc0496aa403781fd3001b9560b4a6984d27066b30b12a0a462198ec268546fe10e024e0ae100aea062c61bd2c328ad44eea1800894b1ffc82157b8726702f71be6066143394eb7855c90c7f12b83a6842b8a2aa23eb8c5b8e5109c80cc011fec5038281f1bcb568599f72e4f3dfdc129270ee4b72946ff9f66; auth=\"2|1:0|10:1658844030|4:auth|556:YjQ0OTAwMmZhYjFkNTA5OWQ1MmVhYzcxYWM3NTY5NDk3OWZjMWNjOGM1NjEzZDgzOGRjMDE3YzliZjcyOTQyODg1NDBhM2ZkZjIzNjg4NDMyZmZmYWU4OTdmMzYyZmM0MjE3YzIzZmJlODZkNmUzMzNmOGJhNDA2OTE2ZGJhZDUzN2UwY2UxZGEyNzg2N2YwNWZjZjQ5NzUwZGEyNzZlNmEyOGNmOWExNTcyZGMxZmMwNDk2YWE0MDM3ODFmZDMwMDFiOTU2MGI0YTY5ODRkMjcwNjZiMzBiMTJhMGE0NjIxOThlYzI2ODU0NmZlMTBlMDI0ZTBhZTEwMGFlYTA2MmM2MWJkMmMzMjhhZDQ0ZWVhMTgwMDg5NGIxZmZjODIxNTdiODcyNjcwMmY3MWJlNjA2NjE0MzM5NGViNzg1NWM5MGM3ZjEyYjgzYTY4NDJiOGEyYWEyM2ViOGM1YjhlNTEwOWM4MGNjMDExZmVjNTAzODI4MWYxYmNiNTY4NTk5ZjcyZTRmM2RmZGMxMjkyNzBlZTRiNzI5NDZmZjlmNjY=|bffee7da1104ef32330b3a19dad8f6fc3bde58c472334344b693c204fbfe0783\"; redirect_url=\"https://999.md/cabinet/favorites?&subcategory_url=real-estate/apartments-and-rooms\"")

	httpRequest := &http.Request{
		Method: "GET",
		URL:    urlObj,
		Header: reqHeader,
	}

	resp, reqError := httpClient.Do(httpRequest)
	if reqError != nil {
		panic(reqError)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			panic(err)
		}
	}(resp.Body)

	data, dataErr := io.ReadAll(resp.Body)
	if dataErr != nil {
		panic(dataErr)
	}

	if resp.StatusCode != 200 {
		panic("Page failed: " + resp.Status)
	}

	return data, nil
}

func getXsrf() []byte {
	responseFile := "login_page.txt"

	//response, fileError := ioutil.ReadFile(responseFile)
	//if fileError != nil {
	getLoginPage()
	response2, fileError2 := ioutil.ReadFile(responseFile)
	if fileError2 != nil {
		panic(fileError2)
	}
	//response = response2
	//}

	return parseXsrf(response2)
}

func getLoginPage() []*http.Cookie {
	client := http.Client{}

	pageUrl := url.URL{
		Scheme: "https",
		Host:   "simpalsid.com",
		Path:   "/user/login?project_id=999a46c6-e6a6-11e1-a45f-28376188709b&lang=ru",
	}
	// view-source:https://simpalsid.com/user/login?project_id=999a46c6-e6a6-11e1-a45f-28376188709b&lang=ru

	req := &http.Request{
		Method: "GET",
		URL:    &pageUrl,
		Header: http.Header{},
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/103.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}

	//fileError := ioutil.WriteFile("login_page.txt", []byte(string(content)), 0644)
	//if fileError != nil {
	//	panic(fileError)
	//}

	return resp.Cookies()
}

func writeToFile(filepath string, data []byte) {
	fileError := ioutil.WriteFile(filepath, data, 0644)
	if fileError != nil {
		panic(fileError)
	}
}

func fileExists(filepath string) (bool, error) {
	_, err := os.Stat(filepath)
	if err == nil {
		return true, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	return false, nil
}

func getFileData(filepath string) []byte {
	data, err := ioutil.ReadFile(filepath)
	if err != nil {
		panic(err)
	}

	return data
}

// curl 'https://simpalsid.com/user/login?project_id=999a46c6-e6a6-11e1-a45f-28376188709b&lang=ru' \
// -H 'authority: simpalsid.com' \
// -H 'accept: text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.9' \
// -H 'accept-language: en-GB,en-US;q=0.9,en;q=0.8,ru;q=0.7,bg;q=0.6,ro;q=0.5,fr;q=0.4' \
// -H 'cache-control: max-age=0' \
// -H 'content-type: application/x-www-form-urlencoded' \
// -H 'cookie: _xsrf=2|e0ccc3b1|67c653807c8fd4d7f1d8a1576813cd1c|1651835299; auth="2|1:0|10:1658820209|4:auth|556:MTJiMTViZWI4NWM3MjkxZWZhY2RjYjI2ZmMyNjFkODk1MTQxOGU0MmJiYzVmNjY4ZTc1NzdjMDlhZjg1YTJiZGM4YjBjNGQ4YmVmNjZhMDgxMDgyYWI1ZGRlY2UwYTRiZWJlY2RmOTkxNDAxZGJiZjY5MGVlOTE2MTY3M2E1NDY5Zjg3YWQ3OGE1ZDM5ZjdiZWIyMGM2NDE1YWM3Y2U5OWEyODI5OThiMzkxNTdkYzZkMGJlYWZhMTNlNzQ1ODAyZmIxM2E1MTFjNDA5NmRlNGM4NmFkZDE5MDUzMTBmMzE3NWZiMDU0YjlmNmQ0MTdiZGUwODU4YmYxNzEyMmY3MmQ1NThiNjVkMGFiODc3ZWVkNDhjNWJiYzliM2UxZGQ3NWQyMmFmMDg3NTdkMjE3MTA5ODQ1NDc5MGQ5MDllYTVlN2MzZTY2YWMyMTU5OTljMDg2M2EzODZjNTI3NDJkNjYzZTg1Yjk0NTViMzAzYjJiOTYzZTUzNGEzNzUzNTVkNmVmYjFjNTM0Y2YzOGY5ZjRlNTFmODZhMDQ3MmFhODA=|4bee2dbdc361e9a3c61a792cfdc5aa7d106e3716c1659da594fc43552ff33e98"; redirect_url="https://999.md/ru/"; lang="2|1:0|10:1658825970|4:lang|4:cnU=|54654d3a5d64baaaef35cd9c1d3c9bd82a21f5c2d43b0298053593678505752f"' \
// -H 'origin: https://simpalsid.com' \
// -H 'referer: https://simpalsid.com/user/login?project_id=999a46c6-e6a6-11e1-a45f-28376188709b&lang=ru' \
// -H 'sec-ch-ua: ".Not/A)Brand";v="99", "Google Chrome";v="103", "Chromium";v="103"' \
// -H 'sec-ch-ua-mobile: ?0' \
// -H 'sec-ch-ua-platform: "macOS"' \
// -H 'sec-fetch-dest: document' \
// -H 'sec-fetch-mode: navigate' \
// -H 'sec-fetch-site: same-origin' \
// -H 'sec-fetch-user: ?1' \
// -H 'upgrade-insecure-requests: 1' \
// -H 'user-agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/103.0.0.0 Safari/537.36' \
// --data-raw '_xsrf=2%7C53a60639%7Cd4ac9608cfe5115f42b264dfdb790894%7C1651835299&redirect_url=https%3A%2F%2F999.md%2Fru%2F&login=sdfdsf%40sddf.dd&password=qweewrwer' \
// --compressed
func login(cookies []*http.Cookie) (string, error) {
	xsrf := ""
	for _, cookie := range cookies {
		if cookie.Name == "_xsrf" {
			xsrf = cookie.Value
		}
	}

	client := http.Client{}

	pageUrl := url.URL{
		Scheme: "https",
		Host:   "simpalsid.com",
		Path:   "/user/login?project_id=999a46c6-e6a6-11e1-a45f-28376188709b&lang=ru",
	}

	//formValues := url.Values{
	//	"_xsrf":        {xsrf},
	//	"redirect_url": {"https://999.md/ru/"},
	//	"login":        {"turcaigor@gmail.com"},
	//	"password":     {"1q2aw3zse4"},
	//}

	formValues := url.Values{}
	formValues.Add("_xsrf", xsrf)
	formValues.Add("redirect_url", "https://999.md/ru/")
	formValues.Add("login", "turcaigor@gmail.com")
	formValues.Add("password", "1q2aw3zse4")

	req := &http.Request{
		Method: "POST",
		URL:    &pageUrl,
		Form:   formValues,
		Header: http.Header{},
	}

	//apiUrl := "https://simpalsid.com"
	//resource := "/user/login?project_id=999a46c6-e6a6-11e1-a45f-28376188709b&land=ru"
	//data := url.Values{}
	////data.Set("project_id", "999a46c6-e6a6-11e1-a45f-28376188709b")
	////data.Set("lang", "ru")
	//data.Set("_xsrf", xsrf)
	//data.Set("redirect_url", "https://999.md/ru/")
	//data.Set("login", "turcaigor@gmail.com")
	//data.Set("password", "1q2aw3zse4")

	//u, _ := url.ParseRequestURI(apiUrl)
	//u.Path = resource
	//urlStr := u.String() // "https://api.com/user/"
	//urlStr := "https://simpalsid.com/user/login?project_id=999a46c6-e6a6-11e1-a45f-28376188709b&lang=ru"

	//client := &http.Client{}
	//req, _ := http.NewRequest(http.MethodPost, urlStr, strings.NewReader(data.Encode())) // URL-encoded payload
	//r.Header.Add("Authorization", "auth_token=\"XXXXXXX\"")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/103.0.0.0 Safari/537.36")

	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	//req.AddCookie(&http.Cookie{
	//	Name:  "auth",
	//	Value: "2|1:0|10:1658820209|4:auth|556:MTJiMTViZWI4NWM3MjkxZWZhY2RjYjI2ZmMyNjFkODk1MTQxOGU0MmJiYzVmNjY4ZTc1NzdjMDlhZjg1YTJiZGM4YjBjNGQ4YmVmNjZhMDgxMDgyYWI1ZGRlY2UwYTRiZWJlY2RmOTkxNDAxZGJiZjY5MGVlOTE2MTY3M2E1NDY5Zjg3YWQ3OGE1ZDM5ZjdiZWIyMGM2NDE1YWM3Y2U5OWEyODI5OThiMzkxNTdkYzZkMGJlYWZhMTNlNzQ1ODAyZmIxM2E1MTFjNDA5NmRlNGM4NmFkZDE5MDUzMTBmMzE3NWZiMDU0YjlmNmQ0MTdiZGUwODU4YmYxNzEyMmY3MmQ1NThiNjVkMGFiODc3ZWVkNDhjNWJiYzliM2UxZGQ3NWQyMmFmMDg3NTdkMjE3MTA5ODQ1NDc5MGQ5MDllYTVlN2MzZTY2YWMyMTU5OTljMDg2M2EzODZjNTI3NDJkNjYzZTg1Yjk0NTViMzAzYjJiOTYzZTUzNGEzNzUzNTVkNmVmYjFjNTM0Y2YzOGY5ZjRlNTFmODZhMDQ3MmFhODA=|4bee2dbdc361e9a3c61a792cfdc5aa7d106e3716c1659da594fc43552ff33e98",
	//})

	resp, err := client.Do(req)
	//resp, err := http.PostForm("https://simpalsid.com/user/login?project_id=999a46c6-e6a6-11e1-a45f-28376188709b&lang=ru", formValues)
	if err != nil {
		panic(err)
	}

	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}

	fmt.Println(content)

	return "asd", nil
}

func parseXsrf(html []byte) []byte {
	re := regexp.MustCompile(`(?i)<input type=\"hidden\" name=\"_xsrf\" value=\"([^"]+).*>`)
	xsrf := re.FindSubmatch(html)
	if len(xsrf) < 2 {
		panic("Xsrf not found")
	}

	return xsrf[1]
}

func parseViews(text string) int {
	re := regexp.MustCompile(`^Просмотры: ([0-9\s]+) \(сегодня ([0-9\s]+)\)$`)
	result := re.FindStringSubmatch(text)
	if len(result) < 3 {
		panic("Xsrf not found")
	}

	views, _ := strconv.Atoi(strings.ReplaceAll(result[1], " ", ""))
	//todayViews, _ := strconv.Atoi(result[2])

	return views
}
