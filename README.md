## twbot package

[![Go Report Card](https://goreportcard.com/badge/github.com/dns-gh/twbot)](https://goreportcard.com/report/github.com/dns-gh/twbot)

[![GoDoc](https://godoc.org/github.com/dns-gh/twbot?status.png)]
(https://godoc.org/github.com/dns-gh/twbot)

Twitter Bot providing an asynchronous API to:
- Make simple tweets
- Retweet messages with a user defined pattern
- Auto like tweets/retweets with a user-defined pattern
- Auto follow the followers of a user
- Auto unfollow friends with a user-defined pattern
- Add user-defined randomness to avoid, in a way, being caught as a bot

Still more to do, feel free to join my efforts!

## Motivation

Used in the Nasa Space Rocks Bot project https://github.com/dns-gh/nasa-space-rocks-bot and for potential futur projects with Twitter for me or others :)

## Installation

- It requires Go language of course. You can set it up by downloading it here: https://golang.org/dl/
- Install it here C:/Go.
- Set your GOPATH, GOROOT and PATH environment variables:

```
export GOROOT=C:/Go
export GOPATH=WORKING_DIR
export PATH=C:/Go/bin:${PATH}
```

- Download and install the package:

```
@working_dir $ go get github.com/dns-gh/twbot/...
@working_dir $ go install github.com/dns-gh/twbot
```

## Example

See the https://github.com/dns-gh/nasa-space-rocks-bot

## Tests

TODO

## LICENSE

See included LICENSE file.