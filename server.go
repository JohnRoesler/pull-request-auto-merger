package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type Settings struct {
	GitHubUserName         string `yaml:"gitHubUserName"`
	GitHubToken            string `yaml:"gitHubToken"`
	RestrictMergeRequester string `yaml:"restrictMergeRequester"`
}

var settings Settings

type EventPullRequest struct {
	URL string `json:"url"`
}

type User struct {
	Login string `json:"login"`
}

type Comment struct {
	HTMLurl string `json:"html_url"`
	Body    string `json:"body"`
	User    User   `json:"user"`
}

type Issue struct {
	Number           int              `json:"number"`
	State            string           `json:"state"`
	EventPullRequest EventPullRequest `json:"pull_request"`
	HTMLurl          string           `json:"html_url"`
}

type Repository struct {
	FullName string `json:"full_name"`
}

type IssueCommentWebhookEvent struct {
	Issue      Issue      `json:"issue"`
	Repository Repository `json:"repository"`
	Comment    Comment    `json:"comment"`
}

type Head struct {
	Sha string `json:"sha"`
}

type PullRequest struct {
	URL       string `json:"url"`
	Head      Head   `json:"head"`
	Mergeable bool   `json:"mergeable"`
	Title     string `json:"title"`
	User      User   `json:"user"`
}

type ApiResponse struct {
	Body       []byte
	StatusCode int
	Error      error
}

type stop struct {
	error
}

const mergeComment = "please merge"
const gitHubApiBaseUrl = "https://api.github.com"

func retry(attempts int, sleep time.Duration, f func() ApiResponse) ApiResponse {
	apiResponse := f()
	if apiResponse.Error != nil {
		if s, ok := apiResponse.Error.(stop); ok {
			// if it's a stop return the original error for later checking
			apiResponse.Error = s.error
			return apiResponse
		}

		if attempts--; attempts > 0 {
			time.Sleep(sleep)
			return retry(attempts, 2*sleep, f)
		}
		return apiResponse
	}

	return apiResponse
}

type ApiCall func(url string, method string, payload string, settings Settings) ApiResponse

func apiCall(url string, method string, payload string, settings Settings) ApiResponse {
	req, err := http.NewRequest(method, url, strings.NewReader(payload))
	if err != nil {
		return ApiResponse{Body: nil, StatusCode: -1, Error: err}
	}

	basicAuthToken := base64.StdEncoding.EncodeToString([]byte(settings.GitHubUserName + ":" + settings.GitHubToken))

	req.Header.Add("Authorization", "Basic "+basicAuthToken)
	req.Header.Add("content-type", "application/json")

	return retry(3, time.Second, func() ApiResponse {
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return ApiResponse{Body: nil, StatusCode: -1, Error: err}
		}

		defer res.Body.Close()

		body, err := ioutil.ReadAll(res.Body)
		if err != nil {
			// this results in a retry as we're passing back
			return ApiResponse{Body: body, StatusCode: res.StatusCode, Error: err}
		}

		s := res.StatusCode
		switch {
		case s >= 500:
			// Retry
			return ApiResponse{Body: body, StatusCode: res.StatusCode, Error: fmt.Errorf("server error: %v", s)}
		case s >= 400:
			// Don't retry, it was client's fault
			return ApiResponse{Body: body, StatusCode: res.StatusCode, Error: stop{fmt.Errorf("client error: %v", s)}}
		default:
			// Happy
			return ApiResponse{Body: body, StatusCode: res.StatusCode, Error: nil}
		}

	})

}

func autoMerge(event IssueCommentWebhookEvent, apiCall ApiCall) string {
	if event.Issue.State != "open" {
		return "Pull request is not open."
	}

	// get info about the pull request
	urlPR := fmt.Sprintf("%s/repos/%s/pulls/%d", gitHubApiBaseUrl, event.Repository.FullName, event.Issue.Number)
	prApiResponse := apiCall(urlPR, "GET", "", settings)
	if prApiResponse.Error != nil {
		log.Printf("Failed to get the pull request details: %s", prApiResponse.Error)
		return "Error fetching pull request details. Try again."
	}

	var pr PullRequest
	err := json.Unmarshal(prApiResponse.Body, &pr)
	if err != nil {
		log.Println(err)
		return "Error fetching pull request details. Try again."
	}

	if !pr.Mergeable {
		return "Pull Request is not mergeable. Make sure there is approval and status checks have passed."
	}

	// by default, the request to merge comment will only be honored if the opener of the PR makes the comment
	// if merging is restricted to the requester, check comment user
	var restrictBool bool
	if settings.RestrictMergeRequester != "" {
		restrictBool, err = strconv.ParseBool(settings.RestrictMergeRequester)
	} else {
		// env not set, default to true
		restrictBool = true
	}
	if restrictBool == true && pr.User.Login != event.Comment.User.Login {
		return "Merge request comment must be made by the pull request author."
	}

	// try to merge the pr
	urlMerge := fmt.Sprintf("%s/repos/%s/pulls/%d/merge", gitHubApiBaseUrl, event.Repository.FullName, event.Issue.Number)
	payload := fmt.Sprintf(`{
	"commit_title": "%s",
	"commit_message": "PR automatically merged",
	"sha": "%s",
	"merge_method": "squash"
	}`, pr.Title, pr.Head.Sha)

	mergeApiResponse := apiCall(urlMerge, "PUT", payload, settings)

	log.Printf("Response: %d %s", mergeApiResponse.StatusCode, mergeApiResponse.Body)

	type Body struct {
		Message string `json:"message"`
	}

	var responseMessage Body

	err = json.Unmarshal(mergeApiResponse.Body, &responseMessage)
	if err != nil {
		log.Println(err)
		return "Error fetching merge request response details."
	}

	message := strings.Replace(responseMessage.Message, `"`, "'", -1)

	switch mergeApiResponse.StatusCode {
	case 200:
		log.Printf("Merged pull request: %s", pr.URL)
		return ""
	case 405, 409:
		return message
	default:
		log.Printf("Unexpected response from pull request merge api, %d %s", mergeApiResponse.StatusCode, mergeApiResponse.Body)
		return message
	}
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, fmt.Sprintf("Method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	b, err := ioutil.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		http.Error(w, fmt.Sprintf("Request body could not be read, %s", err.Error()), http.StatusInternalServerError)
		return
	}

	var event IssueCommentWebhookEvent
	err = json.Unmarshal(b, &event)
	if err != nil {
		http.Error(w, fmt.Sprintf("Could not unmarshal body, %s", err.Error()), http.StatusInternalServerError)
		return
	}

	// no errors with request, so send a 200 and then do stuff
	_, err = io.WriteString(w, "OK")
	if err != nil {
		// log an error, but keep going, doesn't really matter if a response makes it back
		log.Println(fmt.Errorf("Error sending response back to GitHub webhook, %s", err))
	}

	// check if comment is what we're looking for, otherwise do nothing
	if strings.ToLower(event.Comment.Body) != mergeComment {
		log.Printf("Comment was not '%s', url: %s.", mergeComment, event.Comment.HTMLurl)
		return
	}

	// if it's an issue and not a pull request, do nothing
	if event.Issue.EventPullRequest.URL == "" {
		log.Printf("Event triggered on issue and not pull request, url: %s.", event.Comment.HTMLurl)
		return
	}

	comment := autoMerge(event, apiCall)

	if comment != "" {
		// comment back on the pr
		log.Printf("Commenting on PR #%d in: %s with comment: %s, url: %s", event.Issue.Number, event.Repository.FullName, comment, event.Issue.HTMLurl)
		urlComment := fmt.Sprintf("%s/repos/%s/issues/%d/comments", gitHubApiBaseUrl, event.Repository.FullName, event.Issue.Number)
		payload := fmt.Sprintf(`{
		"body": "%s"
		}`, comment)
		commentApiResponse := apiCall(urlComment, "POST", payload, settings)
		if commentApiResponse.Error != nil {
			log.Printf("Failed to comment on the pull request: %s with failure reason: %s %s", event.Issue.HTMLurl, commentApiResponse.Error, commentApiResponse.Body)
		}
	}
}

func health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, fmt.Sprintf("Method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	_, err := io.WriteString(w, "OK")
	if err != nil {
		log.Println(fmt.Errorf("Error sending response to health check, %s", err))
		return
	}
	log.Println("Request made to /health")
}

func main() {
	// don't verify when calling to GitHub, otherwise we need a cert bundle
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	log.Println("Server starting...")

	settings.GitHubUserName = os.Getenv("GITHUB_USERNAME")
	settings.GitHubToken = os.Getenv("GITHUB_TOKEN")
	settings.RestrictMergeRequester = os.Getenv("RESTRICT_MERGE_REQUESTER")

	if settings.GitHubToken == "" || settings.GitHubUserName == "" {
		log.Fatalf("GitHub username or token not set, cannot start application.")
	}

	port := "8080"

	http.HandleFunc("/", handleRequest)
	http.HandleFunc("/health", health)
	log.Printf("Server started, listening on port %s", port)
	log.Print(http.ListenAndServe(":"+port, nil))
}
