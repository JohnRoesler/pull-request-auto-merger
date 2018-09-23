# pull-request-auto-merger
A simple server that merges pull requests when requested

## How it works

The server listens for the `Issue Comments` webhook event and will merge the pull request if the following are met:

- the comment is `please merge`
- all required status checks have passed
- all required approvals received
- comment made by the pull request author (configurable default)

## How to use

Deploy and run the app and then configure a GitHub repo Webhook to act on the event `Issue comments`.

Open a pull request and comment `please merge`. If required approvals and status checks have passed, the pull request will be merged.

### Docker
Build the docker image from the `Dockerfile` and deploy the docker image somewhere and set the environment variables:

name | required | default | description
-- | -- | -- | --
`GITHUB_USERNAME` | yes | n/a | Used for GitHub API calls - must have write access to the repo
`GITHUB_TOKEN` | yes | n/a | Used for GitHub API calls - must have write access to the repo
`RESTRICT_MERGE_REQUESTER` | no | `true` | Restricts the merge comment request to the PR author when true

## Local Development
Build Go binary
```
go build server.go
```

To run in docker:

Build the docker image
```
docker build -t merger .
```

Run it
```
docker run -e GITHUB_TOKEN=<token> -e GITHUB_USERNAME=<username>  -p 8080:8080 --rm merger
```

## Golang dependencies

Managed via `dep`.

Run a `dep ensure` before committing to new code to have any packages added to the vendor folder and Gopkg lock and toml
