package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/charmbracelet/log"
	"github.com/dofusdude/dodugo"
	mapping "github.com/dofusdude/dodumap"
	"github.com/google/go-github/v67/github"
	"golang.org/x/exp/rand"
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

const (
	AlmanaxUrl               = "https://www.krosmoz.com/en/almanax"
	DoduapiUpdateEndpointUrl = "https://api.dofusdu.de/dofus3/v1/update"
	UserAgent                = "Mozilla/5.0 (Windows NT 6.1; rv:2.0b7) Gecko/20100101 Firefox/4.0b7"
	DataRepoOwner            = "dofusdude"
	DataRepoName             = "dofus3-main"
	MappedAlmanaxFileName    = "MAPPED_ALMANAX.json"
)

var DoduapiUpdateToken string

// ParseDuration parses a duration string.
// examples: "10d", "-1.5w" or "3Y4M5d".
// Add time units are "d"="D", "w"="W", "M", "y"="Y".
func ParseDuration(s string) (time.Duration, error) {
	neg := false
	if len(s) > 0 && s[0] == '-' {
		neg = true
		s = s[1:]
	}

	re := regexp.MustCompile(`(\d*\.\d+|\d+)[^\d]*`)
	unitMap := map[string]time.Duration{
		"d": 24,
		"D": 24,
		"w": 7 * 24,
		"W": 7 * 24,
		"M": 30 * 24,
		"y": 365 * 24,
		"Y": 365 * 24,
	}

	strs := re.FindAllString(s, -1)
	var sumDur time.Duration
	for _, str := range strs {
		var _hours time.Duration = 1
		for unit, hours := range unitMap {
			if strings.Contains(str, unit) {
				str = strings.ReplaceAll(str, unit, "h")
				_hours = hours
				break
			}
		}

		dur, err := time.ParseDuration(str)
		if err != nil {
			return 0, err
		}

		sumDur += dur * _hours
	}

	if neg {
		sumDur = -sumDur
	}
	return sumDur, nil
}

func loadAlmanaxData(version string) ([]mapping.MappedMultilangNPCAlmanaxUnity, error) {
	client := github.NewClient(nil)

	repRel, _, err := client.Repositories.GetReleaseByTag(context.Background(), DataRepoOwner, DataRepoName, version)
	if err != nil {
		return nil, err
	}

	// get the mapped almanax data
	var assetId int64
	assetId = -1
	for _, asset := range repRel.Assets {
		if asset.GetName() == MappedAlmanaxFileName {
			assetId = asset.GetID()
			break
		}
	}

	if assetId == -1 {
		return nil, fmt.Errorf("could not find asset with name %s", MappedAlmanaxFileName)
	}

	log.Info("downloading asset", "assetId", assetId)
	httpClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Automatically follow all redirects
			return nil
		},
	}
	asset, redirectUrl, err := client.Repositories.DownloadReleaseAsset(context.Background(), DataRepoOwner, DataRepoName, assetId, httpClient)
	if err != nil {
		return nil, err
	}

	if asset == nil {
		return nil, fmt.Errorf("asset is nil, redirect url: %s", redirectUrl)
	}

	defer asset.Close()

	var almData []mapping.MappedMultilangNPCAlmanaxUnity
	dec := json.NewDecoder(asset)
	err = dec.Decode(&almData)
	if err != nil {
		return nil, err
	}

	return almData, nil
}

func updateAlmanaxRelease(almData []mapping.MappedMultilangNPCAlmanaxUnity, version string, ghToken string) error {
	client := github.NewClient(nil).WithAuthToken(ghToken)

	repRel, _, err := client.Repositories.GetReleaseByTag(context.Background(), DataRepoOwner, DataRepoName, version)
	if err != nil {
		return err
	}

	// delete the old asset
	for _, asset := range repRel.Assets {
		if asset.GetName() == MappedAlmanaxFileName {
			_, err = client.Repositories.DeleteReleaseAsset(context.Background(), "dofusdude", "dofus3-main", asset.GetID())
			if err != nil {
				return err
			}
		}
	}

	// create the new asset
	assetName := MappedAlmanaxFileName
	assetLabel := MappedAlmanaxFileName
	assetContentType := "application/json"
	assetDataBytes, err := json.MarshalIndent(almData, "", "  ")
	if err != nil {
		return err
	}

	// write to file
	assetFile, err := os.Create("tmp.json")
	if err != nil {
		return err
	}
	defer assetFile.Close()

	_, err = assetFile.Write(assetDataBytes)
	if err != nil {
		return err
	}

	assetFile, err = os.Open("tmp.json")
	if err != nil {
		return err
	}

	defer func() {
		assetFile.Close()
		_ = os.Remove("tmp.json")
	}()

	_, _, err = client.Repositories.UploadReleaseAsset(context.Background(), DataRepoOwner, DataRepoName, repRel.GetID(), &github.UploadOptions{
		Name:      assetName,
		Label:     assetLabel,
		MediaType: assetContentType,
	}, assetFile)
	if err != nil {
		return err
	}

	if DoduapiUpdateToken != "" {
		body := fmt.Sprintf(`{"version":"%s"}`, version)
		req, err := http.NewRequest("POST", fmt.Sprintf("%s/%s", DoduapiUpdateEndpointUrl, DoduapiUpdateToken), strings.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		_, err = http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
	}

	return err
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

func getAlmOfferingReceiver(date string) string {
	almUrl := fmt.Sprintf("%s/%s?game=dofus", AlmanaxUrl, date)
	req, err := http.NewRequest("GET", almUrl, nil)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("User-Agent", UserAgent)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Error("error sending request, waiting and trying again", "err", err, "url", almUrl, "date", date)
		time.Sleep(1 * time.Minute)
		return getAlmOfferingReceiver(date)
	}
	defer res.Body.Close()

	if res.StatusCode == 202 {
		log.Info("date not yet available, waiting and trying again")
		time.Sleep(1 * time.Minute)
		return getAlmOfferingReceiver(date)
	}

	if res.StatusCode != 200 {
		log.Fatalf("status code error: %d %s", res.StatusCode, res.Status)
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Fatal(err)
	}

	var receiver string
	expr := regexp.MustCompile(`Quest: Offering for (\w+)`)
	matches := expr.FindStringSubmatch(doc.Text())
	if len(matches) > 1 {
		receiver = matches[1]
	}
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
	RewardKamas    int    `json:"reward_kamas"`
}

func loadLocalVersion(workdir string) (string, error) {
	path := path.Join(workdir, "version")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer file.Close()

	var version string
	_, err = fmt.Fscanf(file, "%s", &version)
	if err != nil {
		return "", err
	}

	return version, nil
}

func saveLocalVersion(version string, workdir string) error {
	path := path.Join(workdir, "version")
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = fmt.Fprintf(file, "%s", version)
	if err != nil {
		return err
	}

	return nil
}

func updateChan(ctx context.Context, interval time.Duration, update chan string, workdir string, readyForUpdate chan bool) {
	serverUrl := "https://api.dofusdu.de"
	game := "dofus3"
	cfg := &dodugo.Configuration{
		DefaultHeader: make(map[string]string),
		UserAgent:     "dofusdude/alm-dates",
		Debug:         false,
		Servers: dodugo.ServerConfigurations{
			{
				URL:         serverUrl,
				Description: "API",
			},
		},
		OperationServers: map[string]dodugo.ServerConfigurations{},
	}

	timer := time.NewTicker(interval)

	isReady := true

	for {
		select {
		case <-ctx.Done():
			return
		case receivedReady := <-readyForUpdate:
			isReady = receivedReady
		case <-timer.C:
			if !isReady {
				continue
			}

			var dodugoClient = dodugo.NewAPIClient(cfg)

			version, http, err := dodugoClient.MetaAPI.GetMetaVersion(ctx, game).Execute()
			if err != nil {
				log.Fatal("error getting meta version: ", err)
				return
			}

			if http != nil && http.StatusCode != 200 {
				log.Fatal("error getting meta version", "status", http.Status)
				return
			}

			currentApiVersion := version.GetVersion()
			localVersion, err := loadLocalVersion(workdir)
			if err != nil {
				log.Fatal("error loading local version: ", err)
				return
			}

			if currentApiVersion != localVersion {
				err = saveLocalVersion(*version.Version, workdir)
				if err != nil {
					log.Fatal("error saving local version: ", err)
					return
				}
				update <- currentApiVersion
			}
		}
	}
}

func parseWd(dir string) (string, error) {
	var err error

	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	if dir[:1] == "." {
		dir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, os.ModePerm)
		if err != nil {
			return "", err
		}
	}

	return dir, nil
}

func main() {
	cwd := os.Getenv("PWD")
	var err error
	if cwd == "" {
		cwd, err = parseWd(".")
	} else {
		cwd, err = parseWd(cwd)
	}
	if err != nil {
		log.Fatal("error parsing working directory: ", "error", err)
	}

	ghAuthKey := os.Getenv("GH_AUTH_KEY")
	if ghAuthKey == "" {
		log.Fatal("no github auth key found")
	}

	DoduapiUpdateToken = os.Getenv("DODUAPI_UPDATE_TOKEN")

	pollIntervalStr := os.Getenv("POLLING_INTERVAL")
	if pollIntervalStr == "" {
		pollIntervalStr = "1m"
	}

	endDurationStr := os.Getenv("END_DURATION")
	if endDurationStr == "" {
		endDurationStr = "1y"
	}

	endDuration, err := ParseDuration(endDurationStr)
	if err != nil {
		log.Fatal("error parsing end duration: ", "error", err)
	}

	pollIerval, err := time.ParseDuration(pollIntervalStr)
	if err != nil {
		log.Fatal("error parsing polling interval: ", "error", err)
	}

	update := make(chan string)
	context := context.Background()
	readyForUpdate := make(chan bool)
	go updateChan(context, pollIerval, update, cwd, readyForUpdate)

	for {
		select {
		case <-context.Done():
			return
		case version := <-update:

			readyForUpdate <- false
			log.Info("update detected", "version", version)

			func() {
				defer func() {
					readyForUpdate <- true
					log.Info("ready for next update")
				}()

				almData, err := loadAlmanaxData(version)
				if err != nil {
					log.Fatal("error loading almanax data: ", "error", err)
				}

				// map the data
				today := time.Now()
				inYear := today.Add(endDuration)
				fromDate := today.Format("2006-01-02")
				toDate := inYear.Format("2006-01-02")

				dateRange := createDateRange(fromDate, toDate)

				if len(almData[0].Days) != 0 && almData[0].Days[0] != "" {
					log.Info("data already mapped, skipping", "version", version)
					return
				}

				log.Info("Mapping...")
				start := time.Now()

				for _, date := range dateRange {
					offeringReceiverKrozmoz := getAlmOfferingReceiver(date)

					found := false
					for i, almDataLocal := range almData {
						if almDataLocal.OfferingReceiver == offeringReceiverKrozmoz {
							found = true
							almData[i].Days = append(almData[i].Days, date)
							break
						}
					}
					if !found {
						log.Fatal("could not find offering receiver: ", offeringReceiverKrozmoz)
					}

					time.Sleep(time.Duration(rand.Intn(2)+1) * time.Second)
				}

				log.Info("Mapping done", "duration", time.Since(start))

				err = updateAlmanaxRelease(almData, version, ghAuthKey)
				if err != nil {
					log.Fatal("error updating almanax release: ", err)
				}
			}()

		}
	}
}
