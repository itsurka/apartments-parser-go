package dto

type Apartment struct {
	Id                  int
	URL                 string
	Title               string
	Desc                string
	PriceEur            int
	PriceUsd            int
	PriceLeu            int
	PriceSquareMeterEur int
	Location            string
	LastUpdated         string
	PageViews           int
	SellerLogin         string
	SellerPhone         string
	ImageUrls           []string
	Available           bool
}
