package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// --- Constants ---
const (
	// Use a clean base URL.
	googleSearchURLBase   = "https://www.google.com/search"
	maxPagesToScrape      = 2 // Keep it VERY low to avoid being blocked
	retryAttempts         = 3
	retryDelay            = 5 * time.Second
	nameSelector          = ".e2BEnf.hAyfcb .AP7Wnd"             // Selector for name (needs refining)
	profileLinkSelector   = "a[href*='linkedin.com/in/']"         // Robust profile link selector
	googleSnippetSelector = ".VwiC3b.yXK7lf.MUxGbd.yDYNvb.lyLwlc.lEBKkf" // Selector for Google snippet

	// Regex patterns
	emailRegex      = `[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`
	phoneRegex      = `\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}` // Basic US phone number regex (adapt as needed)
	experienceRegex = `(\d+)\s+year[s]?`                     // Regex to extract experience in years

	outputFilename = "linkedin_candidates.csv" // CSV output filename
)

// --- Structs ---
type Candidate struct {
	Name       string `json:"name"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	ProfileURL string `json:"profile_url"`
	Experience int    `json:"experience"` // Experience in years, if found
}

// buildGoogleSearchURL constructs the Google search URL using the provided criteria.
func buildGoogleSearchURL(keywords, location, industry, experienceRange string) string {
	// Build the query string.
	query := fmt.Sprintf("site:linkedin.com/in %s %s %s %s", keywords, location, industry, experienceRange)
	params := url.Values{}
	params.Add("q", query)
	searchURL := googleSearchURLBase + "?" + params.Encode()
	return searchURL
}

// scrapeGoogleSearchResults processes the Google search results page and extracts candidate data.
func scrapeGoogleSearchResults(doc *goquery.Document) ([]Candidate, error) {
	var candidates []Candidate

	doc.Find(".tF2Cxc").Each(func(i int, s *goquery.Selection) {
		// Get the LinkedIn profile link.
		profileLink, ok := s.Find(profileLinkSelector).Attr("href")
		if !ok {
			return
		}

		// Clean the profile link using regex.
		re := regexp.MustCompile(`(https:\/\/www\.linkedin\.com\/in\/[^&?]+)`)
		match := re.FindStringSubmatch(profileLink)
		if len(match) > 1 {
			profileLink = match[1]
		} else {
			return
		}

		// Extract the name using the specified selector.
		name := strings.TrimSpace(s.Find(nameSelector).Text())

		// Extract email, phone, and experience from the snippet.
		snippet := s.Find(googleSnippetSelector).Text()
		email := extractRegex(snippet, emailRegex)
		phone := extractRegex(snippet, phoneRegex)
		experience, _ := parseExperience(snippet)

		candidate := Candidate{
			Name:       name,
			ProfileURL: profileLink,
			Email:      email,
			Phone:      phone,
			Experience: experience,
		}
		candidates = append(candidates, candidate)
	})

	return candidates, nil
}

// scrapeProfileDetails visits the LinkedIn profile page to extract additional details.
func scrapeProfileDetails(profileURL string) (Candidate, error) {
	var candidate Candidate
	candidate.ProfileURL = profileURL

	// Use random delay to mimic human behavior.
	delay := time.Duration(rand.Intn(10)+5) * time.Second // Delay between 5 and 14 seconds.
	time.Sleep(delay)
	client := getProxyClient() // Use proxy client if available.

	req, err := http.NewRequest("GET", profileURL, nil)
	if err != nil {
		return candidate, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers.
	req.Header = getHeaders()
	req.Header.Set("Referer", "https://www.google.com/")

	resp, err := client.Do(req)
	if err != nil {
		return candidate, fmt.Errorf("failed to fetch profile: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == 429 || resp.StatusCode == 302 {
			log.Println("Encountered potential CAPTCHA or rate limit. Stopping.")
			return candidate, fmt.Errorf("captcha or rate limit")
		}
		return candidate, fmt.Errorf("profile request failed with status: %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return candidate, fmt.Errorf("failed to parse profile HTML: %w", err)
	}

	// For public profiles, the selector might be different.
	nameSelectorPublic := ".top-card-layout__title" // Example selector (adjust as needed).
	candidate.Name = strings.TrimSpace(doc.Find(nameSelectorPublic).Text())

	// Attempt to extract email and phone via regex from the entire page HTML.
	html, _ := doc.Html()
	candidate.Email = extractRegex(html, emailRegex)
	candidate.Phone = extractRegex(html, phoneRegex)

	return candidate, nil
}

// extractRegex extracts a substring matching the regex from the provided text.
func extractRegex(text, regex string) string {
	re := regexp.MustCompile(regex)
	return re.FindString(text)
}

// parseExperience extracts the experience in years from a text snippet.
func parseExperience(experienceStr string) (int, error) {
	re := regexp.MustCompile(experienceRegex)
	match := re.FindStringSubmatch(experienceStr)
	if len(match) > 1 {
		years, err := strconv.Atoi(match[1])
		if err != nil {
			return 0, fmt.Errorf("error parsing experience years: %w", err)
		}
		return years, nil
	}
	return 0, fmt.Errorf("experience not found in string: %s", experienceStr)
}

// getProxyClient returns an HTTP client configured to use a proxy if valid proxies are provided.
// If no valid proxy is available, it returns the default HTTP client.
func getProxyClient() *http.Client {
	// If you have proxies, add valid proxy URLs here.
	proxyList := []string{} // Leave empty if you don't need a proxy.
	if len(proxyList) == 0 {
		return &http.Client{Timeout: 10 * time.Second}
	}

	proxyURL, err := url.Parse(proxyList[rand.Intn(len(proxyList))])
	if err != nil {
		log.Println("Invalid proxy URL:", err)
		return &http.Client{Timeout: 10 * time.Second}
	}

	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	return client
}

// getHeaders returns HTTP headers including a random User-Agent.
func getHeaders() http.Header {
	userAgents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Safari/605.1.15",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:89.0) Gecko/20100101 Firefox/89.0",
	}
	headers := http.Header{}
	headers.Set("User-Agent", userAgents[rand.Intn(len(userAgents))])
	return headers
}

// writeToCSV writes the list of candidates to a CSV file.
func writeToCSV(candidates []Candidate, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header row.
	header := []string{"Name", "Email", "Phone", "Profile URL", "Experience"}
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("failed to write header row: %w", err)
	}

	// Write candidate rows.
	for _, candidate := range candidates {
		row := []string{
			candidate.Name,
			candidate.Email,
			candidate.Phone,
			candidate.ProfileURL,
			strconv.Itoa(candidate.Experience),
		}
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("failed to write data row: %w", err)
		}
	}

	return nil
}

func main() {
	rand.Seed(time.Now().UnixNano())

	// --- Configuration ---
	// Searching for LinkedIn profiles of professionals who:
	// - Work with "control valve desuperheater"
	// - Are based in Bangalore
	// - Operate in the "Machinery Manufacturing" industry
	// - Have 7-12 years of experience
	keywords := "control valve desuperheater"
	location := "Bangalore"
	industry := "Machinery Manufacturing"
	experienceRange := "7-12 years"

	// Build the Google search URL.
	searchURL := buildGoogleSearchURL(keywords, location, industry, experienceRange)
	fmt.Printf("Searching Google with URL: %s\n", searchURL)

	var allCandidates []Candidate
	for page := 0; page < maxPagesToScrape; page++ {
		fmt.Printf("Scraping Google page %d...\n", page+1)
		pageURL := searchURL
		if page > 0 {
			// Google uses the 'start' parameter for pagination.
			pageURL = fmt.Sprintf("%s&start=%d", searchURL, page*10)
		}

		// Random delay between requests.
		delay := time.Duration(rand.Intn(10)+5) * time.Second
		fmt.Printf("Waiting for %.0f seconds before scraping page %d\n", delay.Seconds(), page+1)
		time.Sleep(delay)

		client := getProxyClient()
		var resp *http.Response
		var err error

		// Retry logic for fetching the page.
		for attempt := 0; attempt < retryAttempts; attempt++ {
			req, reqErr := http.NewRequest("GET", pageURL, nil)
			if reqErr != nil {
				log.Printf("Error creating request: %v", reqErr)
				continue
			}
			req.Header = getHeaders()
			req.Header.Set("Referer", "https://www.google.com/")

			resp, err = client.Do(req)
			if err != nil {
				log.Printf("Error fetching page: %v. Retrying in %.0f seconds", err, retryDelay.Seconds())
				time.Sleep(retryDelay)
				continue
			}
			if resp.StatusCode != http.StatusOK {
				log.Printf("Received status code %d. Retrying in %.0f seconds", resp.StatusCode, retryDelay.Seconds())
				resp.Body.Close()
				time.Sleep(retryDelay)
				continue
			}
			break
		}
		if err != nil {
			log.Printf("Failed to fetch page %d: %v", page+1, err)
			continue
		}

		doc, err := goquery.NewDocumentFromReader(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("Error parsing page %d: %v", page+1, err)
			continue
		}

		candidates, err := scrapeGoogleSearchResults(doc)
		if err != nil {
			log.Printf("Error scraping candidates from page %d: %v", page+1, err)
			continue
		}

		// Optionally, scrape additional details from each candidate's LinkedIn profile.
		for i, cand := range candidates {
			fmt.Printf("Scraping details for candidate %d: %s\n", i+1, cand.ProfileURL)
			detailedCandidate, err := scrapeProfileDetails(cand.ProfileURL)
			if err != nil {
				log.Printf("Error scraping profile details for %s: %v", cand.ProfileURL, err)
			} else {
				// Update candidate details if profile scraping succeeds.
				cand.Name = detailedCandidate.Name
				cand.Email = detailedCandidate.Email
				cand.Phone = detailedCandidate.Phone
			}
			candidates[i] = cand
		}

		allCandidates = append(allCandidates, candidates...)
	}

	if len(allCandidates) == 0 {
		log.Println("No candidates found.")
		return
	}

	if err := writeToCSV(allCandidates, outputFilename); err != nil {
		log.Fatalf("Error writing CSV: %v", err)
	}

	fmt.Printf("Successfully wrote %d candidates to %s\n", len(allCandidates), outputFilename)
}
