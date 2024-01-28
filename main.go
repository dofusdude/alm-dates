package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/charmbracelet/log"
	mapping "github.com/dofusdude/dodumap"
)

func isDate(date string) bool {
	if len(date) != 10 {
		return false
	}
	if date[4] != '-' || date[7] != '-' {
		return false
	}

	// check if date is valid
	var month, day, year int
	_, err := fmt.Sscanf(date, "%d-%d-%d", &year, &month, &day)
	if err != nil {
		return false
	}
	if month < 1 || month > 12 {
		return false
	}
	if day < 1 || day > 31 {
		return false
	}

	return true
}

func loadNoDateData() []mapping.MappedMultilangNPCAlmanax {
	req, err := http.NewRequest("GET", "https://api.github.com/repos/dofusdude/dofus2-main/releases/latest", nil)
	if err != nil {
		fmt.Println("error creating request: ", err)
		os.Exit(1)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal("error sending request: ", err)
	}

	js := json.NewDecoder(resp.Body)
	var data map[string]interface{}
	err = js.Decode(&data)
	if err != nil {
		log.Fatal("error decoding response body: ", err)
	}

	dofusVersion := data["tag_name"].(string)
	mappedUrl := fmt.Sprintf("https://github.com/dofusdude/dofus2-main/releases/download/%s/MAPPED_ALMANAX.json", dofusVersion)
	req, err = http.NewRequest("GET", mappedUrl, nil)
	if err != nil {
		log.Fatal("error creating request: ", err)
	}

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal("error sending request: ", err)
	}

	var almDataNoDate []mapping.MappedMultilangNPCAlmanax
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&almDataNoDate)
	if err != nil {
		log.Fatal("error decoding response body: ", err)
	}

	// check that every key is unique with a map
	uniqueLookup := make(map[string]bool)
	for _, alm := range almDataNoDate {
		if uniqueLookup[alm.OfferingReceiver] {
			log.Fatal("duplicate key found: ", alm.OfferingReceiver)
		}
		uniqueLookup[alm.OfferingReceiver] = true
	}

	return almDataNoDate
}

func createDateRange(fromDate string, toDate string) []string {
	start, err := time.Parse("2006-01-02", fromDate)
	if err != nil {
		fmt.Println("error parsing from date: ", err)
		os.Exit(1)
	}

	end, err := time.Parse("2006-01-02", toDate)
	if err != nil {
		fmt.Println("error parsing to date: ", err)
		os.Exit(1)
	}

	var dateRange []string
	for current := start; current.Before(end) || current.Equal(end); current = current.AddDate(0, 0, 1) {
		dateRange = append(dateRange, current.Format("2006-01-02"))
	}

	return dateRange
}

func loadFileLastTime(appendix string) (map[string]mapping.MappedMultilangNPCAlmanax, bool) {
	fileName := fmt.Sprintf("alm-prev-%s.json", appendix)
	fileLastTime, err := os.Open(fileName)
	if err != nil {
		return make(map[string]mapping.MappedMultilangNPCAlmanax), false
	}

	var almDataLastTime map[string]mapping.MappedMultilangNPCAlmanax
	dec := json.NewDecoder(fileLastTime)
	err = dec.Decode(&almDataLastTime)
	if err != nil {
		log.Fatal("error decoding last.json: ", err)
	}

	return almDataLastTime, true
}

func saveFileLastTime(almDataWithDate map[string]mapping.MappedMultilangNPCAlmanax, appendix string) {
	fileName := fmt.Sprintf("alm-prev-%s.json", appendix)
	fileLastTime, err := os.Create(fileName)
	if err != nil {
		log.Fatal("error creating last-time-file: ", err)
	}

	enc := json.NewEncoder(fileLastTime)
	err = enc.Encode(almDataWithDate)
	if err != nil {
		log.Fatal("error encoding last-time-file: ", err)
	}
}

func getAlmOfferingReceiver(date string) string {
	almUrl := fmt.Sprintf("https://www.krosmoz.com/en/almanax/%s?game=dofus", date)
	req, err := http.NewRequest("GET", almUrl, nil)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 6.1; rv:2.0b7) Gecko/20100101 Firefox/4.0b7")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode > 214 {
		log.Fatalf("status code error: %d %s", res.StatusCode, res.Status)
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Fatal(err)
	}

	var receiver string
	doc.Find("#achievement_dofus > div.mid > div > div > p").Each(func(i int, s *goquery.Selection) {
		// check that the string starts with "Quest: Offering for "
		if strings.HasPrefix(s.Text(), "Quest: Offering for ") {
			receiver = s.Text()[20:] // delete "Quest: Offering for " from string
		}
	})
	return receiver
}

type AlmApiData struct {
	Date           string `json:"date"`
	ItemQuantity   int    `json:"item_quantity"`
	ItemName       string `json:"item"`
	Bonus          string `json:"description"`
	BonusType      string `json:"bonus"`
	Language       string `json:"language"`
	ItemPictureUrl string `json:"item_picture_url"`
}

func addUpdateApi(almData mapping.MappedMultilangNPCAlmanax, date string, authKey string) {
	initLang := "en"
	almApiDataInit := AlmApiData{
		Date:           date,
		ItemQuantity:   almData.Offering.Quantity,
		ItemName:       almData.Offering.ItemName[initLang],
		Bonus:          almData.Bonus[initLang],
		BonusType:      almData.BonusType[initLang],
		Language:       initLang,
		ItemPictureUrl: almData.Offering.ImageUrls.Icon,
	}

	createUpdateEndpointUrl := "https://alm.dofusdu.de/dofus2/almanax"

	almApiData, err := json.Marshal(almApiDataInit)
	if err != nil {
		log.Fatal(err)
	}
	req, err := http.NewRequest("POST", createUpdateEndpointUrl, bytes.NewBuffer(almApiData))
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+authKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode > 214 && res.StatusCode != 406 {
		b, err := io.ReadAll(res.Body)
		if err != nil {
			log.Fatal(err)
		}

		log.Fatal("init en creation error", "code", res.StatusCode, "status", res.Status, "response", string(b))
	}

	if res.StatusCode == 406 { // exists, so put to same endpoint
		req, err := http.NewRequest("PUT", createUpdateEndpointUrl, bytes.NewBuffer(almApiData))
		if err != nil {
			log.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+authKey)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Fatal(err)
		}
		defer res.Body.Close()

		if res.StatusCode > 214 {
			log.Fatalf("updating en failed: %d %s", res.StatusCode, res.Status)
		}
	}

	lanugages := []string{"de", "fr", "es", "it"}
	for _, language := range lanugages {
		almApiDataInit.Language = language
		almApiDataInit.ItemName = almData.Offering.ItemName[language]
		almApiDataInit.Bonus = almData.Bonus[language]
		almApiDataInit.BonusType = almData.BonusType[language]
		almApiData, err := json.Marshal(almApiDataInit)
		if err != nil {
			log.Fatal(err)
		}
		req, err := http.NewRequest("PUT", createUpdateEndpointUrl, bytes.NewBuffer(almApiData))
		if err != nil {
			log.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+authKey)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Fatal(err)
		}
		defer res.Body.Close()

		if res.StatusCode > 214 {
			log.Fatalf("%s update error: %d %s", language, res.StatusCode, res.Status)
		}
	}
}

func main() {
	fromDate := os.Args[1]
	if !isDate(fromDate) {
		log.Fatal("from date is not valid")
	}
	toDate := os.Args[2]
	if !isDate(toDate) {
		log.Fatal("to date is not valid")
	}
	appendix := os.Args[3]
	authKey := os.Args[4]
	if authKey == "" {
		log.Fatal("auth key is not valid")
	}
	dateRange := createDateRange(fromDate, toDate)
	log.Info("load file from last time")
	almDataLastTime, _ := loadFileLastTime(appendix)
	log.Info("load Almanax from GitHub...")
	almDataNoDate := loadNoDateData()

	almDataWithDate := make(map[string]mapping.MappedMultilangNPCAlmanax)

	for _, date := range dateRange {
		log.Info("next...", "date", date)
		lastTimeData := mapping.MappedMultilangNPCAlmanax{}
		if value, ok := almDataLastTime[date]; ok {
			lastTimeData = value
		}

		offeringReceiverKrozmoz := getAlmOfferingReceiver(date)

		found := false
		for _, almData := range almDataNoDate {
			if almData.OfferingReceiver == offeringReceiverKrozmoz {
				found = true
				almDataWithDate[date] = almData
				break
			}
		}
		if !found {
			log.Fatal("could not find offering receiver: ", offeringReceiverKrozmoz)
		}

		if offeringReceiverKrozmoz == lastTimeData.OfferingReceiver {
			continue
		}

		log.Info("add/update api")
		addUpdateApi(almDataWithDate[date], date, authKey)
		time.Sleep(time.Duration(rand.Intn(2)+1) * time.Second)
	}

	saveFileLastTime(almDataWithDate, appendix)
}
