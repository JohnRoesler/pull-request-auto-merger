package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var apiResponsePRDetails ApiResponse
var apiResponseMerge ApiResponse

func mockApiCall(url string, method string, payload string, s Settings) ApiResponse {
	if method == "GET" {
		return apiResponsePRDetails
	} else {
		return apiResponseMerge
	}
}

func TestAutoMerge(t *testing.T) {

	openIssueCommentWebhookEvent := IssueCommentWebhookEvent{
		Issue: Issue{
			EventPullRequest: EventPullRequest{
				URL: "https://url.com",
			},
			State: "open",
		},
		Comment: Comment{
			User: User{
				Login: "JimmyD",
			},
		},
	}
	closedIssueCommentWebhookEvent := IssueCommentWebhookEvent{
		Issue: Issue{
			EventPullRequest: EventPullRequest{
				URL: "https://url.com",
			},
			State: "closed",
		},
	}

	prResponseDefault := ApiResponse{
		Body:       []byte(`{"mergeable": true, "url": "https://url.com", "head": {"sha": "1234"}, "user":{"login":"JimmyD"}}`),
		StatusCode: 200,
		Error:      nil,
	}

	type TestCase struct {
		name                 string
		event                IssueCommentWebhookEvent
		apiResponsePRDetails ApiResponse
		apiResponseMerge     ApiResponse
		expectedComment      string
	}
	testCases := []TestCase{
		TestCase{
			name:  "Comment if pull request is not open",
			event: closedIssueCommentWebhookEvent,
			apiResponsePRDetails: ApiResponse{
				Body:       []byte(""),
				StatusCode: 200,
				Error:      nil,
			},
			expectedComment: "Pull request is not open.",
		},
		TestCase{
			name:  "Error handled if received from pull request GET",
			event: openIssueCommentWebhookEvent,
			apiResponsePRDetails: ApiResponse{
				Body:       []byte(""),
				StatusCode: 500,
				Error:      fmt.Errorf("Some error"),
			},
			expectedComment: "Error fetching pull request details. Try again.",
		},
		TestCase{
			name:  "Error thrown unmarshalling bad json",
			event: openIssueCommentWebhookEvent,
			apiResponsePRDetails: ApiResponse{
				Body:       []byte(""),
				StatusCode: 200,
				Error:      nil,
			},
			expectedComment: "Error fetching pull request details. Try again.",
		},
		TestCase{
			name:  "Respond when not mergeable",
			event: openIssueCommentWebhookEvent,
			apiResponsePRDetails: ApiResponse{
				Body:       []byte(`{"mergeable": false, "url": "https://url.com", "head": {"sha": "1234"}}`),
				StatusCode: 200,
				Error:      nil,
			},
			expectedComment: "Pull Request is not mergeable. Make sure there is approval and status checks have passed.",
		},
		TestCase{
			name:  "Respond that PR creator must request merge",
			event: openIssueCommentWebhookEvent,
			apiResponsePRDetails: ApiResponse{
				Body:       []byte(`{"mergeable": true, "url": "https://url.com", "head": {"sha": "1234"}, "user":{"login":"SomeGuy"}}`),
				StatusCode: 200,
				Error:      nil,
			},
			expectedComment: "Merge request comment must be made by the pull request author.",
		},
		TestCase{
			name:                 "Expect empty comment from 200 response on merge attempt",
			event:                openIssueCommentWebhookEvent,
			apiResponsePRDetails: prResponseDefault,
			apiResponseMerge: ApiResponse{
				Body:       []byte("{}"),
				StatusCode: 200,
				Error:      nil,
			},
			expectedComment: "",
		},
		TestCase{
			name:                 "Expect appropriate comment from 405 response on merge attempt",
			event:                openIssueCommentWebhookEvent,
			apiResponsePRDetails: prResponseDefault,
			apiResponseMerge: ApiResponse{
				Body:       []byte(`{"message":"Required status check \"continuous-integration/drone/pr\" is failing. At least 1 approving review is required by reviewers with write access.","documentation_url":"https://help.github.com/enterprise/2.14/user/articles/about-protected-branches"}`),
				StatusCode: 405,
				Error:      nil,
			},
			expectedComment: "Required status check 'continuous-integration/drone/pr' is failing. At least 1 approving review is required by reviewers with write access.",
		},
		TestCase{
			name:                 "Expect appropriate comment from 409 response on merge attempt",
			event:                openIssueCommentWebhookEvent,
			apiResponsePRDetails: prResponseDefault,
			apiResponseMerge: ApiResponse{
				Body:       []byte(`{"message":"Head branch was modified. Review and try the merge again.","documentation_url":"https://developer.github.com/v3/pulls/#merge-a-pull-request-merge-button"}`),
				StatusCode: 409,
				Error:      nil,
			},
			expectedComment: "Head branch was modified. Review and try the merge again.",
		},
		TestCase{
			name:                 "Handle unexpected status code response from merge attempt",
			event:                openIssueCommentWebhookEvent,
			apiResponsePRDetails: prResponseDefault,
			apiResponseMerge: ApiResponse{
				Body:       []byte(`{"message":"Internal Server Error."}`),
				StatusCode: 500,
				Error:      nil,
			},
			expectedComment: "Internal Server Error.",
		},
		TestCase{
			name:  "Allow merge by non author if restrict merge is false",
			event: openIssueCommentWebhookEvent,
			apiResponsePRDetails: ApiResponse{
				Body:       []byte(`{"mergeable": true, "url": "https://url.com", "head": {"sha": "1234"}, "user":{"login":"SomeGuy"}}`),
				StatusCode: 200,
				Error:      nil,
			},
			apiResponseMerge: ApiResponse{
				Body:       []byte("{}"),
				StatusCode: 200,
				Error:      nil,
			},
			expectedComment: "",
		},
	}
	for _, tc := range testCases {
		apiResponsePRDetails = tc.apiResponsePRDetails
		apiResponseMerge = tc.apiResponseMerge
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "Allow merge by non author if restrict merge is false" {
				settings.RestrictMergeRequester = "false"
			}

			comment := autoMerge(tc.event, mockApiCall)
			if comment != tc.expectedComment {
				t.Fatalf("Expected comment to be: %s, found: %s", tc.expectedComment, comment)
			}
		})
	}
}

func TestHandleRequest(t *testing.T) {
	issueCommentWebhookEvent := IssueCommentWebhookEvent{
		Issue: Issue{
			Number: 1,
			State:  "open",
			EventPullRequest: EventPullRequest{
				URL: "https://url.com",
			},
		},
		Comment: Comment{
			Body: "Please Merge",
		},
		Repository: Repository{
			FullName: "JohnRoesler/test",
		},
	}

	jsonBody, err := json.Marshal(issueCommentWebhookEvent)
	if err != nil {
		t.Fatalf("failed to marshall json")
	}

	t.Run("Should return 200 on POST with valid body", func(t *testing.T) {
		req, err := http.NewRequest("POST", "localhost:8080", bytes.NewBuffer(jsonBody))
		if err != nil {
			t.Fatalf("could not create request: %v", err)
		}

		rec := httptest.NewRecorder()

		handleRequest(rec, req)

		res := rec.Result()
		if res.StatusCode != http.StatusOK {
			t.Errorf("expected status OK, got %s", res.Status)
		}
	})

	t.Run("Should return 405 on non-POST method", func(t *testing.T) {

		for _, method := range []string{"GET", "PUT", "HEAD", "TRACE"} {
			req, err := http.NewRequest(method, "localhost:8080", bytes.NewBuffer(jsonBody))
			if err != nil {
				t.Fatalf("could not create request: %v", err)
			}

			rec := httptest.NewRecorder()

			handleRequest(rec, req)

			res := rec.Result()
			if res.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("expected status 405 Method Not Allowed, got %s", res.Status)
			}
		}

	})
}

func TestRetry(t *testing.T) {
	t.Run("Should retry twice per passed attempt value 2", func(t *testing.T) {
		counter := 0
		retry(2, time.Second, func() ApiResponse {
			counter++
			return ApiResponse{Error: fmt.Errorf("some error")}
		})
		if counter != 2 {
			t.Fatalf("Expected to be retried once. Got %d", counter)
		}
	})

	t.Run("Should retry once if a stop is returned by first function", func(t *testing.T) {
		counter := 0
		retry(2, time.Second, func() ApiResponse {
			counter++
			return ApiResponse{Error: stop{fmt.Errorf("STOP")}}
		})
		if counter != 1 {
			t.Fatalf("Expected to be retried once. Got %d", counter)
		}
	})

	t.Run("Should not retry if no error is returned", func(t *testing.T) {
		counter := 0
		retry(2, time.Second, func() ApiResponse {
			counter++
			return ApiResponse{Error: nil}
		})
		if counter != 1 {
			t.Fatalf("Expected to be retried once. Got %d", counter)
		}
	})
}
