package models

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// LegistarAction represents a single action taken on legislation
type LegistarAction struct {
	Date      time.Time `json:"date,omitempty"`
	Actor     string    `json:"actor,omitempty"`
	Action    string    `json:"action,omitempty"`
	Committee string    `json:"committee,omitempty"`
}

// Bill is a single bill object in the database
type Bill struct {
	PK             uint   `gorm:"primary_key"`
	TweetID        *int64 `json:"tweet_id,omitempty"`
	TweetUser      string `gorm:"size:250" json:"tweet_user"`
	TweetText      string `gorm:"size:300" json:"tweet_text"`
	LastTweetID    *int64 `json:"last_tweet_id,omitempty"`
	BillID         string `gorm:"size:25" json:"id,omitempty"`
	Title          string `gorm:"size:250" json:"title"`
	Classification string `gorm:"size:250" json:"classification"`
	URL            string `gorm:"size:250" json:"url"`
	Active         bool   `gorm:"default:true"`
	Data           string `gorm:"type:text"`
	NextRun        *time.Time
}

// ParseBillID pulls the bill identifier from a given string
func (b *Bill) ParseBillID(text string) string {
	billRe := regexp.MustCompile(`(^| )[a-zA-Z]{1,4}[\-\s]*\d{4}[\-\s]*\d{1,5}`)
	spacerRe := regexp.MustCompile(`[\s-]+`)
	billText := strings.TrimSpace(billRe.FindString(text))
	billUpper := strings.ToUpper(spacerRe.ReplaceAllLiteralString(billText, ""))
	// Special exception for Orders
	return strings.Replace(billUpper, "OR", "Or", 1)
}

// GetCleanBillID returns the bill ID in a format for presentation
func (b *Bill) GetCleanBillID() string {
	// Return bill ID in format for API
	billRe := regexp.MustCompile(`(?P<type>[A-Za-z]+)(?P<year>\d{4})(?P<id>\d+)`)
	billMatch := billRe.FindStringSubmatch(b.BillID)
	result := make(map[string]string)
	for i, name := range billRe.SubexpNames() {
		if i != 0 && name != "" {
			result[name] = billMatch[i]
		}
	}
	return fmt.Sprintf("%s%s-%s", result["type"], result["year"], result["id"])
}

// GetActions loads all actions from a bill's JSON data as LegistarAction objects
func (b *Bill) GetActions() []LegistarAction {
	var actions []LegistarAction

	err := json.Unmarshal([]byte(b.Data), &actions)
	if err != nil {
		log.Fatal(err)
	}
	return actions
}

// SearchBill tries to find the detail page for a bill from Legistar
func (b *Bill) SearchBill() (string, error) {
	response, err := http.Get("https://cityofcleveland.legistar.com/Legislation.aspx")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	document, err := goquery.NewDocumentFromReader(response.Body)
	if err != nil {
		return "", err
	}

	viewstate, _ := document.Find("input[name='__VIEWSTATE']").First().Attr("value")
	eventvalidation, _ := document.Find("input[name='__EVENTVALIDATION']").First().Attr("value")
	payload := url.Values{
		"__EVENTARGUMENT":                                        {""},
		"__VIEWSTATE":                                            {viewstate},
		"__EVENTVALIDATION":                                      {eventvalidation},
		"ctl00$ContentPlaceHolder1$txtFil":                       {b.GetCleanBillID()},
		"ctl00_ContentPlaceHolder1_lstMax_ClientState":           {"{\"value\":\"1000000\"}"},
		"ctl00_ContentPlaceHolder1_lstYearsAdvanced_ClientState": {"{\"value\":\"All\"}"},
		"ctl00$ContentPlaceHolder1$btnSearch":                    {"Search Legislation"},
	}
	response, err = http.PostForm("https://cityofcleveland.legistar.com/Legislation.aspx", payload)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	document, err = goquery.NewDocumentFromReader(response.Body)
	if err != nil {
		return "", err
	}

	var billURL string
	document.Find(".rgMasterTable tbody tr > td:first-child a").Each(func(index int, element *goquery.Selection) {
		if element != nil && strings.TrimSpace(element.Text()) == b.GetCleanBillID() {
			billURL, _ = element.Attr("href")
		}
	})

	return fmt.Sprintf("https://cityofcleveland.legistar.com/%s", billURL), nil
}

// FetchBillData pulls updated LegistarAction information from a bill's detail URL
func (b *Bill) FetchBillData() (string, string, []LegistarAction, error) {
	var actions []LegistarAction

	response, err := http.Get(b.URL)
	if err != nil {
		return "", "", actions, err
	}
	defer response.Body.Close()

	document, err := goquery.NewDocumentFromReader(response.Body)
	if err != nil {
		return "", "", actions, err
	}

	title := strings.TrimSpace(document.Find("#ctl00_ContentPlaceHolder1_lblTitle2").First().Text())
	classification := strings.TrimSpace(document.Find("#ctl00_ContentPlaceHolder1_lblType2").First().Text())
	status := strings.TrimSpace(document.Find("#ctl00_ContentPlaceHolder1_lblStatus2").First().Text())
	committee := strings.TrimSpace(document.Find("#ctl00_ContentPlaceHolder1_hypInControlOf2").First().Text())

	document.Find(".rgMasterTable tbody tr").Each(func(index int, element *goquery.Selection) {
		action := LegistarAction{}
		element.Find("td").Each(func(tdIdx int, tdEl *goquery.Selection) {
			if tdIdx == 0 {
				action.Date, _ = time.Parse("1/2/2006", strings.TrimSpace(tdEl.Text()))
			} else if tdIdx == 2 {
				action.Actor = strings.TrimSpace(tdEl.Text())
			} else if tdIdx == 3 {
				action.Action = strings.TrimSpace(tdEl.Text())
				if action.Action == "" {
					action.Action = status
				}
			}
			if strings.Contains(action.Action, "Referred") {
				action.Committee = committee
			}
		})
		actions = append(actions, action)
	})
	return title, classification, actions, nil
}

// @TODO Replace Councilmatic URL check with Legistar default
// GetTweetURL returns the Councilmatic URL if it exists for display and defaults
// to returning the bill's Legistar URL
func (b *Bill) GetTweetURL() string {
	councilmaticURL := fmt.Sprintf(
		"https://cityofcleveland.councilmatic.org/legislation/%s/",
		strings.ToLower(b.GetCleanBillID()),
	)
	response, err := http.Get(councilmaticURL)
	if err != nil {
		return b.URL
	}
	defer response.Body.Close()

	if response.StatusCode == 200 {
		return councilmaticURL
	}
	return b.URL
}

// CreateTweet makes a tweet string based on the bill's information
func (b *Bill) CreateTweet(billURL string) string {
	billID := b.GetCleanBillID()
	billTitle := b.Title
	actions := b.GetActions()
	if billTitle != "" {
		billTitle = fmt.Sprintf("%s: %s", billID, billTitle)
	} else {
		billTitle = billID
	}
	const tweetLen = 280 // Max tweet length
	const urlLen = 23    // Twitter cap for URL characters
	baseChars := len(fmt.Sprintf(" See more at  #%s", b.BillID)) + urlLen

	var actionStr string
	var actionText string
	var action LegistarAction

	// Pull the first action which is the most recent on Legistar, otherwise leave empty
	if len(actions) > 0 {
		action = actions[0]
		actionStr = action.Action
	}

	switch cls := actionStr; cls {
	case "Introduced", "Direct Introduction":
		actionText = fmt.Sprintf(" was introduced in %s", action.Actor)
	case "Placed on File":
		actionText = " was placed on file"
	case "Referred", "Re-Referred":
		if action.Committee != "" {
			actionText = fmt.Sprintf(" was referred to the %s", action.Committee)
		} else {
			actionText = " was referred to committee"
		}
	case "Recommended for Passage":
		actionText = fmt.Sprintf(" was recommended to pass by the %s", action.Actor)
	case "Recommended Do Not Pass":
		actionText = fmt.Sprintf(" was recommended not to pass by the %s", action.Actor)
	case "Recommended for Re-referral":
		actionText = fmt.Sprintf(" was recommended for re-referral by the %s", action.Actor)
	case "Passed", "Passed as Substitute":
		actionText = " passed"
	case "Failed to Pass":
		actionText = " failed to pass"
	case "Introduced (Agreed Calendar)", "Adopted":
		actionText = " was adopted"
	case "Approved", "Repealed", "Vetoed", "Tabled", "Withdrawn":
		actionText = fmt.Sprintf(" was %s", strings.ToLower(cls))
	case "":
	default:
		actionText = fmt.Sprintf(" %s", strings.ToLower(cls))
	}

	tweetContent := fmt.Sprintf("%s%s.", billTitle, actionText)
	if len(tweetContent)+baseChars > tweetLen {
		// Get the difference to remove (add characters for ellipsis)
		tweetDiff := (baseChars + len(tweetContent) + 3) - tweetLen
		var ellipsis string
		// Only include 2 periods for ellipsis if no action text, since it ends with a period
		if actionText == "" {
			ellipsis = ".."
		} else {
			ellipsis = "..."
		}
		tweetContent = fmt.Sprintf("%s%s%s.", strings.TrimSpace(billTitle[0:len(billTitle)-tweetDiff]), ellipsis, actionText)
	}

	return fmt.Sprintf("%s See more at %s #%s", tweetContent, billURL, b.BillID)
}

// SetNextRun updates the time the bill should be queried next
func (b *Bill) SetNextRun() {
	// Set NextRun to a time in the future within a defined range
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Now().Add(time.Hour * time.Duration(12)).In(loc)
	_, offset := now.Zone()

	// Make sure it's between 9AM and 10PM ET with some randomness to stagger posts
	localHour := now.Hour() + (offset / 3600)
	diffHours := 24
	diffMinutes := 0
	if localHour < 9 {
		diffHours = diffHours + (9 - localHour) + rand.Intn(4)
		diffMinutes = rand.Intn(60)
	} else if localHour > 17 {
		diffHours = diffHours - (localHour - 17) - rand.Intn(4)
		diffMinutes = -rand.Intn(60)
	}
	nextRun := now.Add(time.Hour * time.Duration(diffHours)).Add(time.Minute * time.Duration(diffMinutes))
	b.NextRun = &nextRun
}
