package twbot

// TODO:
// - add an errorPolicy ? exported ?
// - get list of suggestions of friendship
// - get list of trending tweets
// - send messages to friends
// - extract the retweet policy and pass it as argument

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	// waiting for https://github.com/ChimeraCoder/anaconda/pull/166 to be merged
	"github.com/dns-gh/anaconda"
	"github.com/dns-gh/freeze"
	"github.com/dns-gh/tojson"
)

const (
	defaultAutoLikeThreshold              = 1000
	defaultMaxRetweetBySearch             = 5 // keep 3 tweets, the 2 first tweets being useless ?
	retweetTextTag                        = "RT @"
	retweetTextIndex                      = ": "
	tweetTCOHTTPTag                       = "http://t.co" // not sure if we can encouter unsecure links with t.co twitter wrapping tool, don't think so...
	tweetTCOHTTPSTag                      = "https://t.co"
	tweetTCOTextIndex                     = " " // either the t.co links is at the end of the tweet or the next separator from what follows is an empty space
	tweetTextMaxSize                      = 140
	tweetTruncatedTextMin                 = 30
	oneDayInNano                    int64 = 86400000000000
	timeSleepBetweenFollowUnFollow        = 300 * time.Second // seconds
	maxRandTimeSleepBetweenRequests       = 120               // seconds
	tcoLinksMaxLength                     = 24
)

type twitterUser struct {
	Timestamp int64 `json:"timestamp"`
	Follow    bool  `json:"follow"`
}

type twitterUsers struct {
	// note: we cannot use integers as keys in encode/json so use string instead
	Ids map[string]*twitterUser `json:"ids"` // map id -> user
}

type likePolicy struct {
	auto      bool
	threshold int
}

type retweetPolicy struct {
	maxTry int
	like   bool
}

// SleepPolicy represents the sleeping behavior of the bot between requests
// to the twitter API. Their use is highly recommanded especially when you
// automatically follow and unfollow users. It will allow you to hide your
// bot face from twitter statistics analysis.
type SleepPolicy struct {
	// maxRand randomly sleeps after each request from '0' to 'maxRand' seconds
	MaxRand int
	// maybeSleep parameters enable the user to create a conditional sleep  after each requests
	// of 'maybeSleepChance' over 'maybeSleepTotalChance' chances. The sleep will last at least
	// 'maybeSleepMin' seconds and a maximum of 'maybeSleepMax' seconds.
	// For example, parameters 1, 10, 30, 60 design a random sleep between 30 to 60 seconds
	// that will occur with a chance of 1 over 10 after each requests.
	MaybeSleepChance      int
	MaybeSleepTotalChance int
	MaybeSleepMin         int
	MaybeSleepMax         int
}

func (s *SleepPolicy) log() {
	log.Printf("[twitter] sleeping policy: %d, %d, %d, %d, %d\n", s.MaxRand, s.MaybeSleepChance,
		s.MaybeSleepTotalChance, s.MaybeSleepMin, s.MaybeSleepMax)
}

// TwitterBot represents the twitter bot.
type TwitterBot struct {
	twitterClient      *anaconda.TwitterApi
	followersPath      string
	followers          *twitterUsers
	friendsPath        string
	friends            *twitterUsers
	tweetsPath         string
	debug              bool
	likePolicy         *likePolicy
	retweetPolicy      *retweetPolicy
	defaultSleepPolicy *SleepPolicy
	mutex              sync.Mutex
	quit               sync.WaitGroup
}

// MakeTwitterBot creates a twitter bot. The database is made of 3 files: followers, friends and tweets.
// They are here to ensure to:
//  - not add a friend as friend
//  - not remove friendship from a non friend
//  - not retweet a tweet already retweeted
//
// You have to set up 4 environment variables:
//  TWITTER_CONSUMER_KEY,
//  TWITTER_CONSUMER_SECRET,
//  TWITTER_ACCESS_TOKEN,
//  TWITTER_ACCESS_SECRET.
// They can be found here by creating a twitter app: https://apps.twitter.com/.
//
// The 'debug' mode creates more logs and remove all sleeps between API twitter calls.
func MakeTwitterBot(followersPath, friendsPath, tweetsPath string, debug bool) *TwitterBot {
	log.Println("[twitter] making twitter bot")
	errorList := []string{}
	consumerKey := getEnv(errorList, "TWITTER_CONSUMER_KEY")
	consumerSecret := getEnv(errorList, "TWITTER_CONSUMER_SECRET")
	accessToken := getEnv(errorList, "TWITTER_ACCESS_TOKEN")
	accessSecret := getEnv(errorList, "TWITTER_ACCESS_SECRET")
	if len(errorList) > 0 {
		log.Fatalln(fmt.Sprintf("errors:\n%s", strings.Join(errorList, "\n")))
	}
	return MakeTwitterBotWithCredentials(followersPath, friendsPath, tweetsPath, consumerKey, consumerSecret, accessToken, accessSecret, debug)
}

// MakeTwitterBotWithCredentials creates a twitter bot.
// Same as MakeTwitterBot but the twitter keys are given as input.
func MakeTwitterBotWithCredentials(followersPath, friendsPath, tweetsPath, consumerKey, consumerSecret, accessToken, accessSecret string, debug bool) *TwitterBot {
	anaconda.SetConsumerKey(consumerKey)
	anaconda.SetConsumerSecret(consumerSecret)
	bot := &TwitterBot{
		twitterClient: anaconda.NewTwitterApi(accessToken, accessSecret),
		followersPath: followersPath,
		followers: &twitterUsers{
			Ids: make(map[string]*twitterUser),
		},
		friendsPath: friendsPath,
		friends: &twitterUsers{
			Ids: make(map[string]*twitterUser),
		},
		tweetsPath: tweetsPath,
		debug:      debug,
		likePolicy: &likePolicy{
			auto:      false,
			threshold: 1000,
		},
		retweetPolicy: &retweetPolicy{
			maxTry: 5,
			like:   true,
		},
		defaultSleepPolicy: &SleepPolicy{
			MaxRand:               maxRandTimeSleepBetweenRequests,
			MaybeSleepChance:      1,
			MaybeSleepTotalChance: 10,
			MaybeSleepMin:         2500,
			MaybeSleepMax:         5000,
		},
	}
	err := bot.updateFollowers()
	if err != nil {
		log.Fatalln(err.Error())
	}
	err = bot.updateFriends()
	if err != nil {
		log.Fatalln(err.Error())
	}
	return bot
}

// Wait waits for all the asynchronous calls to return
func (t *TwitterBot) Wait() {
	t.quit.Wait()
}

// Close closes the twitter client
func (t *TwitterBot) Close() {
	t.twitterClient.Close()
}

// SetLikePolicy sets the like policy that allows to automatically likes tweets
// that are already liked above a threshold.
func (t *TwitterBot) SetLikePolicy(auto bool, threshold int) {
	log.Printf("[twitter] setting like policy -> auto: %t, threshold: %d\n", auto, threshold)
	t.likePolicy.auto = auto
	t.likePolicy.threshold = threshold
}

// SetRetweetPolicy sets the retweet policy that allows to try to retweet 'maxTry' times when looping through
// a list of tweets to retweet. The 'like' parameter controls the ability to like the tweet
// or the retweet using the like policy.
func (t *TwitterBot) SetRetweetPolicy(maxTry int, like bool) {
	log.Printf("[twitter] setting retweet policy -> maxTry: %d, like: %t\n", maxTry, like)
	t.retweetPolicy.maxTry = maxTry
	t.retweetPolicy.like = like
}

// TweetSliceOnce tweets the slice returned by the given 'fetch' callback.
// It returns an error is the 'fetch' calls fails and only logs errors
// for each failed tweet tentative.
func (t *TwitterBot) TweetSliceOnce(fetch func() ([]string, error)) error {
	list, err := fetch()
	if err != nil {
		return err
	}
	for _, msg := range list {
		tweet, err := t.twitterClient.PostTweet(msg, nil)
		if err != nil {
			log.Println(err.Error())
			continue
		}
		log.Println("[twitter] tweeting message (id:", tweet.Id, "):", tweet.Text)
	}
	return nil
}

// TweetSliceOnceAsync tweets asynchronously the slice returned by the
// given 'fetch' callback.
// It logs errors for each failed tweet tentative.
func (t *TwitterBot) TweetSliceOnceAsync(fetch func() ([]string, error)) {
	t.quit.Add(1)
	go func() {
		defer t.quit.Done()
		list, err := fetch()
		if err != nil {
			log.Println(err.Error())
			return
		}
		for _, msg := range list {
			tweet, err := t.twitterClient.PostTweet(msg, nil)
			if err != nil {
				log.Println(err.Error())
				continue
			}
			print(t, fmt.Sprintf("tweeting message (id: %d): %s\n", tweet.Id, tweet.Text))
		}
	}()
}

// TweetSlicePeriodically tweets periodically the slice returned by the given 'fetch' callback.
// The slice tweet frequencies is set up by the given 'freq' input parameter.
// It logs errors for each failed tweet tentative.
func (t *TwitterBot) TweetSlicePeriodically(fetch func() ([]string, error), freq time.Duration) {
	ticker := time.NewTicker(freq)
	defer ticker.Stop()
	for _ = range ticker.C {
		err := t.TweetSliceOnce(fetch)
		if err != nil {
			log.Println(err)
		}
	}
}

// TweetSlicePeriodicallyAsync tweets asynchronously and periodically the
// slice returned by the given 'fetch' callback.
// The slice tweet frequencies is set up by the given 'freq' input parameter.
// It logs errors for each failed tweet tentative.
func (t *TwitterBot) TweetSlicePeriodicallyAsync(fetch func() ([]string, error), freq time.Duration) {
	t.quit.Add(1)
	go func() {
		defer t.quit.Done()
		t.TweetSlicePeriodically(fetch, freq)
	}()
}

// TweetOnce tweets the message returned by the 'fetch' callback.
// It returns an error if the 'fetch' call failed or if the tweet
// itself failed.
func (t *TwitterBot) TweetOnce(fetch func() (string, error)) error {
	msg, err := fetch()
	if err != nil {
		return err
	}
	tweet, err := t.twitterClient.PostTweet(msg, nil)
	if err != nil {
		return err
	}
	print(t, fmt.Sprintf("tweeting message (id: %d): %s\n", tweet.Id, tweet.Text))
	return nil
}

// TweetOnceAsync tweets asynchronously the message returned by the 'fetch' callback.
// It only logs the error if the 'fetch' call failed or if the tweet itself failed.
func (t *TwitterBot) TweetOnceAsync(fetch func() (string, error)) {
	t.quit.Add(1)
	go func() {
		defer t.quit.Done()
		err := t.TweetOnce(fetch)
		if err != nil {
			log.Println(err)
		}
	}()
}

// TweetPeriodically tweets periodically the message returned by the 'fetch' callback.
// The tweet frequencies is set up by the given 'freq' input parameter.
// It only logs the error if the 'fetch' call failed or if the tweet itself failed.
func (t *TwitterBot) TweetPeriodically(fetch func() (string, error), freq time.Duration) {
	ticker := time.NewTicker(freq)
	defer ticker.Stop()
	for _ = range ticker.C {
		err := t.TweetOnce(fetch)
		if err != nil {
			log.Println(err)
		}
	}
}

// TweetPeriodicallyAsync tweets asynchronously and periodically the message returned
// by the 'fetch' callback.
// The tweet frequencies is set up by the given 'freq' input parameter.
// It only logs the error if the 'fetch' call failed or if the tweet itself failed.
func (t *TwitterBot) TweetPeriodicallyAsync(fetch func() (string, error), freq time.Duration) {
	t.quit.Add(1)
	go func() {
		defer t.quit.Done()
		t.TweetPeriodically(fetch, freq)
	}()
}

// we want to truncate under 'tweetTextMaxSize' characters in this preference order:
// - msg + " " + url
// - msg truncated with at least 'tweetTruncatedTextMin' characters + "... " + url
// - url
// - msg
// - truncated msg
func truncate(msg, archiveURL string, urlMaxLength int) string {
	bytes := bytes.NewBufferString(msg).Bytes()
	sep := "... "
	emptySep := " "
	if urlMaxLength == 0 {
		if len(bytes) > tweetTextMaxSize {
			bytes = bytes[0 : tweetTextMaxSize-len(sep)]
			return string(bytes) + sep[0:len(sep)-1]
		}
		return string(bytes)
	}
	if len(bytes)+len(emptySep)+urlMaxLength <= tweetTextMaxSize {
		return string(bytes) + emptySep + archiveURL
	}
	left := len(bytes) + len(sep) + urlMaxLength - tweetTextMaxSize
	// keep at least 'tweetTruncatedTextMin' characters for the message
	if len(bytes)-left >= tweetTruncatedTextMin {
		bytes = bytes[0 : len(bytes)-left]
		return string(bytes) + sep + archiveURL
	}
	if urlMaxLength <= tweetTextMaxSize {
		return archiveURL
	}
	if len(bytes) <= tweetTextMaxSize {
		return string(bytes)
	}
	bytes = bytes[0 : tweetTextMaxSize-1]
	return string(bytes)
}

func (t *TwitterBot) tryPostTweet(msg, archiveURL string, v url.Values) (tweet anaconda.Tweet, err error) {
	tweet, err = t.twitterClient.PostTweet(truncate(msg, archiveURL, tcoLinksMaxLength), v)
	if err != nil {
		if t.isStatusOver140CharactersError(err) {
			tweet, err = t.twitterClient.PostTweet(truncate(msg, archiveURL, len(archiveURL)), v)
			if err != nil {
				return tweet, err
			}
		}
		return tweet, err
	}
	return tweet, nil
}

// TweetImageOnce tweets the given 'msg', 'archiveURL' and img' data provided as strings.
// Note: internally, the 'img' data will be encoded to base 64 in order to be
// properly tweeted via the twitter API.
func (t *TwitterBot) TweetImageOnce(msg, archiveURL, img string) error {
	buf := bytes.NewBufferString(img)
	data := base64.StdEncoding.EncodeToString(buf.Bytes())
	media, err := t.twitterClient.UploadMedia(data)
	if err != nil {
		return err
	}

	v := url.Values{}
	v.Set("media_ids", fmt.Sprintf("%v", media.MediaID))
	tweet, err := t.tryPostTweet(msg, archiveURL, v)
	if err != nil {
		return err
	}
	print(t, fmt.Sprintf("[twitter] tweeting message and image (id: %d): %s\n", tweet.Id, tweet.Text))
	return nil
}

// TweetImagePeriodically tweets periodically the message and image returned
// by the 'fetch' callback.
// The tweet frequencies is set up by the given 'freq' input parameter.
// It only logs the error if the 'fetch' call failed or if the tweet itself failed.
func (t *TwitterBot) TweetImagePeriodically(fetch func() (string, string, string, error), freq time.Duration) {
	ticker := time.NewTicker(freq)
	defer ticker.Stop()
	for _ = range ticker.C {
		msg, img, archive, err := fetch()
		if err != nil {
			log.Println(err)
			continue
		}
		err = t.TweetImageOnce(msg, archive, img)
		if err != nil {
			log.Println(err)
		}
	}
}

// TweetImagePeriodicallyAsync tweets asynchronously and periodically the message and image returned
// by the 'fetch' callback.
// The tweet frequencies is set up by the given 'freq' input parameter.
// It only logs the error if the 'fetch' call failed or if the tweet itself failed.
func (t *TwitterBot) TweetImagePeriodicallyAsync(fetch func() (string, string, string, error), freq time.Duration) {
	t.quit.Add(1)
	go func() {
		defer t.quit.Done()
		t.TweetImagePeriodically(fetch, freq)
	}()
}

// RetweetOnce retweets randomly, with a maximum of 'retweetPolicy.maxTry' tries,
// a tweet matching one element of the input queries slice.
// It returns an error if the loading of tweets in database failed
// or if the retweet itself failed.
func (t *TwitterBot) RetweetOnce(queries, bannedQueries []string) error {
	err := t.autoRetweet(queries, bannedQueries)
	if err != nil {
		return err
	}
	return nil
}

// RetweetOnceAsync retweets asynchronously and randomly, with a maximum of
// 'retweetPolicy.maxTry' tries, a tweet matching one element of the input queries slice.
// It logs errors if the loading of tweets in database failed
// or if the retweets itself failed.
func (t *TwitterBot) RetweetOnceAsync(searchQueries, bannedQueries []string) {
	queries := make([]string, len(searchQueries))
	copy(queries, searchQueries)
	banned := make([]string, len(bannedQueries))
	copy(banned, bannedQueries)
	t.quit.Add(1)
	go func() {
		defer t.quit.Done()
		err := t.RetweetOnce(queries, banned)
		if err != nil {
			log.Println(err)
		}
	}()
}

func (t *TwitterBot) retweetPeriodically(queries, bannedQueries []string, freq time.Duration) {
	ticker := time.NewTicker(freq)
	defer ticker.Stop()
	for _ = range ticker.C {
		err := t.RetweetOnce(queries, bannedQueries)
		if err != nil {
			log.Println(err)
		}
	}
}

// RetweetPeriodically retweets periodically and randomly, with a maximum of
// 'retweetPolicy.maxTry' tries, a tweet matching one element of the input queries slice.
// The retweet frequencies is set up by the given 'freq' input parameter.
// It logs errors if the loading of tweets in database failed
// or if the retweets itself failed.
func (t *TwitterBot) RetweetPeriodically(searchQueries, bannedQueries []string, freq time.Duration) {
	queries := make([]string, len(searchQueries))
	copy(queries, searchQueries)
	banned := make([]string, len(bannedQueries))
	copy(banned, bannedQueries)
	t.retweetPeriodically(queries, banned, freq)
}

// RetweetPeriodicallyAsync retweets asynchronously, periodically and randomly, with a maximum of
// 'retweetPolicy.maxTry' tries, a tweet matching one element of the input queries slice.
// The retweet frequencies is set up by the given 'freq' input parameter.
// It logs errors if the loading of tweets in database failed
// or if the retweets itself failed.
func (t *TwitterBot) RetweetPeriodicallyAsync(searchQueries, bannedQueries []string, freq time.Duration) {
	queries := make([]string, len(searchQueries))
	copy(queries, searchQueries)
	banned := make([]string, len(bannedQueries))
	copy(banned, bannedQueries)
	t.quit.Add(1)
	go func() {
		defer t.quit.Done()
		t.retweetPeriodically(queries, banned, freq)
	}()
}

func (t *TwitterBot) checkSleepPolicy(sleepPolicy *SleepPolicy) SleepPolicy {
	sleepPolicyCopy := *t.defaultSleepPolicy
	if sleepPolicy != nil {
		sleepPolicyCopy = *sleepPolicy
	}
	return sleepPolicyCopy
}

// AutoUnfollowFriendsAsync automatically asynchronously unfollows friends
// from database that were added at least a day ago by default. The sleep policy controls
// the type of sleep you want between requests.
func (t *TwitterBot) AutoUnfollowFriendsAsync(sleepPolicy *SleepPolicy) {
	t.quit.Add(1)
	sleepPolicyCopy := t.checkSleepPolicy(sleepPolicy)
	go func() {
		defer t.quit.Done()
		log.Println("[twitter] launching auto unfollow...")
		sleepPolicyCopy.log()
		t.unfollowAll(&sleepPolicyCopy)
		log.Println("[twitter] auto unfollow disabled")
	}()
}

// AutoFollowFollowers automatically follows the
// followers of the first user fecthed using the given 'query'.
// The 'maxPage' parameter indicates the number of page of followers
// (5000 users max by page) we want to fetch. The sleep policy controls
// the type of sleep you want between requests.
func (t *TwitterBot) AutoFollowFollowers(query string, maxPage int, sleepPolicy SleepPolicy) {
	log.Printf("[twitter] launching auto follow with '%s' over %d page(s)...\n", query, maxPage)
	sleepPolicy.log()
	t.followAll(t.fetchUserIds(query, maxPage), &sleepPolicy)
	log.Println("[twitter] auto follow disabled")
}

// AutoFollowFollowersAsync automatically asynchronously follows the
// followers of the first user fecthed using the given 'query'.
// The 'maxPage' parameter indicates the number of page of followers
// (5000 users max by page) we want to fetch. The sleep policy controls
// the type of sleep you want between requests.
func (t *TwitterBot) AutoFollowFollowersAsync(query string, maxPage int, sleepPolicy *SleepPolicy) {
	t.quit.Add(1)
	sleepPolicyCopy := t.checkSleepPolicy(sleepPolicy)
	go func() {
		defer t.quit.Done()
		t.AutoFollowFollowers(query, maxPage, sleepPolicyCopy)
	}()
}

func (t *TwitterBot) checkAPIError(err error) error {
	if err == nil {
		return err
	}
	apiErr := err.(*anaconda.ApiError)
	if apiErr != nil && apiErr.StatusCode >= 200 && apiErr.StatusCode < 300 {
		print(t, err.Error())
		return nil
	}
	return err
}

func (t *TwitterBot) isStatusOver140CharactersError(err error) bool {
	if err == nil {
		return false
	}
	apiErr := err.(*anaconda.ApiError)
	if apiErr != nil &&
		len(apiErr.Decoded.Errors) > 0 &&
		apiErr.Decoded.Errors[0].Code == anaconda.TwitterErrorStatusOver140Characters {
		print(t, err.Error())
		return true
	}
	return false
}

// UpdateProfileBanner updates the profile banner of the authenticated user
// with the given encoded image data. Other parameters are optionals and usable
// coinjointly only if they all are strictly positive.
// For more details, see: https://dev.twitter.com/rest/reference/post/account/update_profile_banner
func (t *TwitterBot) UpdateProfileBanner(img string, width, height, offsetLeft, offsetTop int) error {
	buf := bytes.NewBufferString(img)
	base64String := base64.StdEncoding.EncodeToString(buf.Bytes())

	v := url.Values{}
	if width > 0 && height > 0 && offsetTop > 0 && offsetLeft > 0 {
		v.Set("width", strconv.Itoa(width))
		v.Set("height", strconv.Itoa(height))
		v.Set("offset_left", strconv.Itoa(offsetLeft))
		v.Set("offset_top", strconv.Itoa(offsetTop))
	}

	return t.checkAPIError(t.twitterClient.AccountUpdateProfileBanner(base64String, v))
}

func getEnv(errorList []string, key string) string {
	value := os.Getenv(key)
	if value == "" {
		errorList = append(errorList, fmt.Sprintf("%q is not defined", key))
	}
	return value
}

func (t *TwitterBot) loadTweets() ([]anaconda.Tweet, error) {
	tweets := &[]anaconda.Tweet{}
	if _, err := os.Stat(t.tweetsPath); os.IsNotExist(err) {
		tojson.Save(t.tweetsPath, tweets)
	}
	err := tojson.Load(t.tweetsPath, tweets)
	if err != nil {
		return nil, err
	}
	return *tweets, nil
}

func stripText(text, tostripped, endSep string) (string, bool) {
	stripped := false
	if strings.Contains(text, tostripped) {
		subtab := strings.SplitN(text, tostripped, 2)
		temp := subtab[0]
		if len(subtab) == 2 {
			subtab2 := strings.SplitN(subtab[1], endSep, 2)
			if len(subtab2) == 2 {
				temp = temp + subtab2[1]
			}
		}
		text = temp
		stripped = true
	}
	return text, stripped
}

func getOriginalText(text string) (string, error) {
	// strip text from retweet prefixes, i.e "RT @name "
	if strings.Contains(text, retweetTextTag) {
		tab := strings.SplitN(text, retweetTextIndex, 2)
		if len(tab) != 2 {
			return "", fmt.Errorf("[twitter] error parsing a tweet text: %s", text)
		}
		text = tab[1]
	}
	// strip text from HTTPS and HTTP t.co links
	stripped := text
	stripped1, stripped2 := false, false
	for {
		stripped, stripped1 = stripText(stripped, tweetTCOHTTPTag, tweetTCOTextIndex)
		stripped, stripped2 = stripText(stripped, tweetTCOHTTPSTag, tweetTCOTextIndex)
		if !stripped1 && !stripped2 {
			break
		}
	}
	return stripped, nil
}

func (t *TwitterBot) takeDifference(previous, current []anaconda.Tweet) []anaconda.Tweet {
	diff := []anaconda.Tweet{}
	addedByID := map[int64]struct{}{}
	addedByText := map[string]struct{}{}
	for _, v := range previous {
		addedByID[v.Id] = struct{}{}
		original, err := getOriginalText(v.Text)
		if err != nil {
			log.Println(err.Error())
		}
		addedByText[original] = struct{}{}
	}
	for _, v := range current {
		if _, ok := addedByID[v.Id]; ok {
			print(t, fmt.Sprintf("[twitter] found a duplicate (same id) from database id:%d, text:%s\n", v.Id, v.Text))
			continue
		}
		original, err := getOriginalText(v.Text)
		if err != nil {
			log.Println(err.Error())
		}
		if _, ok := addedByText[original]; ok {
			print(t, fmt.Sprintf("[twitter] found a duplicate (same original text) from database id:%d, text:%s\n", v.Id, v.Text))
			continue
		}
		addedByID[v.Id] = struct{}{}
		addedByText[original] = struct{}{}
		diff = append(diff, v)
	}
	return diff
}

func (t *TwitterBot) removeDuplicates(current []anaconda.Tweet) []anaconda.Tweet {
	temp := map[string]struct{}{}
	stripped := []anaconda.Tweet{}
	for _, tweet := range current {
		original, err := getOriginalText(tweet.Text)
		if err != nil {
			log.Println(err.Error())
		}
		if _, ok := temp[original]; !ok {
			temp[original] = struct{}{}
			stripped = append(stripped, tweet)
		} else {
			print(t, fmt.Sprintf("[twitter] found a duplicate (id:%d), text:%s\n", tweet.Id, tweet.Text))
		}
	}
	return stripped
}

func (t *TwitterBot) removeBanned(current []anaconda.Tweet, bannedQueries []string) []anaconda.Tweet {
	allowed := []anaconda.Tweet{}
	for _, tweet := range current {
		banned := false
		for _, bannedQuery := range bannedQueries {
			if strings.Contains(tweet.Text, bannedQuery) || strings.Contains(tweet.User.Name, bannedQuery) {
				banned = true
				break
			}
		}
		if !banned {
			allowed = append(allowed, tweet)
		} else {
			print(t, fmt.Sprintf("[twitter] removing banned tweet (id:%d), text:%s\n", tweet.Id, tweet.Text))
		}
	}
	return allowed
}

func (t *TwitterBot) like(tweet *anaconda.Tweet) {
	if !t.likePolicy.auto {
		return
	}
	if tweet.FavoriteCount > t.likePolicy.threshold {
		_, err := t.twitterClient.Favorite(tweet.Id)
		if err != nil {
			print(t, fmt.Sprintf("[twitter] failed to like tweet (id:%d), error: %v\n", tweet.Id, err))
			return
		}
		log.Printf("[twitter] liked tweet (id:%d)\n", tweet.Id)
	} else if tweet.RetweetedStatus != nil &&
		tweet.RetweetedStatus.FavoriteCount > t.likePolicy.threshold {
		t.like(tweet.RetweetedStatus)
	}
}

func print(t *TwitterBot, text string) {
	if t != nil && t.debug {
		log.Println(text)
	}
}

func (t *TwitterBot) sleep() {
	if !t.debug {
		freeze.Sleep(maxRandTimeSleepBetweenRequests)
	}
}

func (t *TwitterBot) maybeSleep(chance, totalChance, min, max int) {
	if !t.debug {
		freeze.MaybeSleepMinMax(chance, totalChance, min, max)
	}
}

func (t *TwitterBot) controlledSleep(sleepPolicy *SleepPolicy) {
	if !t.debug && sleepPolicy != nil {
		freeze.Sleep(sleepPolicy.MaxRand)
		t.maybeSleep(sleepPolicy.MaybeSleepChance, sleepPolicy.MaybeSleepTotalChance,
			sleepPolicy.MaybeSleepMin, sleepPolicy.MaybeSleepMax)
	}
}

func checkBotRestriction(err error) {
	if err != nil {
		strErr := err.Error()
		if strings.Contains(strErr, "Invalid or expired token") ||
			strings.Contains(strErr, "this account is temporarily locked") {
			log.Fatalln(err)
		}
		log.Println(strErr)
	}
}

func (t *TwitterBot) unfollowUser(user *anaconda.User) {
	unfollowed, err := t.twitterClient.UnfollowUserId(user.Id)
	if err != nil {
		checkBotRestriction(err)
		print(t, fmt.Sprintf("[twitter] failed to unfollow user (id:%d, name:%s), error: %v\n", user.Id, user.Name, err))
	}
	log.Printf("[twitter] unfollowing user (id:%d, name:%s)\n", unfollowed.Id, unfollowed.Name)
}

func checkUnableToFollowAtThisTime(err error) bool {
	if err != nil {
		if strings.Contains(err.Error(), "You are unable to follow more people at this time") {
			log.Println("unable to follow at this time, waiting 15min...,", err.Error())
			time.Sleep(15 * time.Minute)
			return true
		}
		return false
	}
	return false
}

func (t *TwitterBot) followUser(user *anaconda.User) {
	followed, err := t.twitterClient.FollowUserId(user.Id, nil)
	if err != nil && !checkUnableToFollowAtThisTime(err) {
		checkBotRestriction(err)
		print(t, fmt.Sprintf("[twitter] failed to follow user (id:%d, name:%s), error: %v\n", user.Id, user.Name, err))
	}
	log.Printf("[twitter] following user (id:%d, name:%s)\n", followed.Id, followed.Name)
}

// retweet retweets the first tweet been able to retweet.
// It returns an error if no retweet has been possible.
func (t *TwitterBot) retweet(current []anaconda.Tweet) (rt anaconda.Tweet, err error) {
	for _, tweet := range current {
		if t.retweetPolicy.like {
			t.like(&tweet)
		}
		retweet, err := t.twitterClient.Retweet(tweet.Id, false)
		if err != nil {
			print(t, fmt.Sprintf("[twitter] failed to retweet tweet (id:%d), error: %v\n", tweet.Id, err))
			t.followUser(&tweet.User)
			continue
		}
		rt = retweet
		if t.retweetPolicy.like {
			t.like(&rt)
		}
		log.Printf("[twitter] retweet (rid:%d, id:%d)\n", rt.Id, tweet.Id)
		t.followUser(&tweet.User)
		return rt, err
	}
	err = fmt.Errorf("unable to retweet")
	return rt, err
}

func (t *TwitterBot) getTweets(queries, bannedQueries []string, previous []anaconda.Tweet) ([]anaconda.Tweet, error) {
	query := freeze.GetRandomElement(queries)
	log.Println("[twitter] searching tweets to retweet with query:", query)
	v := url.Values{}
	v.Set("count", strconv.Itoa(defaultMaxRetweetBySearch))
	results, err := t.twitterClient.GetSearch(query, v)
	if err != nil {
		return nil, err
	}
	current := results.Statuses
	current = t.removeBanned(current, bannedQueries)
	current = t.removeDuplicates(current)
	current = t.takeDifference(previous, current)
	log.Println("[twitter] found", len(current), "tweet(s) to retweet matching pattern")
	return current, nil
}

func (t *TwitterBot) autoRetweet(queries, bannedQueries []string) error {
	count := 0
	previous, err := t.loadTweets()
	if err != nil {
		return err
	}
	for {
		t.sleep()
		tweets, err := t.getTweets(queries, bannedQueries, previous)
		if err != nil {
			return err
		}
		retweeted, err := t.retweet(tweets)
		if err != nil {
			if count < t.retweetPolicy.maxTry {
				count++
				continue
			} else {
				return fmt.Errorf("[twitter] unable to retweet something after %d tries\n", t.retweetPolicy.maxTry)
			}
		}
		previous = append(previous, retweeted)
		tojson.Save(t.tweetsPath, previous)
		return nil
	}
}

func (t *TwitterBot) updateFollowers() error {
	followers := &twitterUsers{
		Ids: make(map[string]*twitterUser),
	}
	if _, err := os.Stat(t.followersPath); os.IsNotExist(err) {
		tojson.Save(t.followersPath, followers)
	}
	err := tojson.Load(t.followersPath, followers)
	if err != nil {
		return err
	}
	for _, v := range followers.Ids {
		v.Follow = false
	}
	for v := range t.twitterClient.GetFollowersIdsAll(nil) {
		for _, id := range v.Ids {
			strID := strconv.FormatInt(id, 10)
			user, ok := followers.Ids[strID]
			if ok {
				user.Follow = true
			} else {
				followers.Ids[strID] = &twitterUser{
					Timestamp: time.Now().UnixNano(),
					Follow:    true,
				}
			}
		}
	}
	err = tojson.Save(t.followersPath, followers)
	if err != nil {
		return err
	}
	t.followers = followers
	return nil
}

func (t *TwitterBot) updateFriends() error {
	friends := &twitterUsers{
		Ids: make(map[string]*twitterUser),
	}
	if _, err := os.Stat(t.friendsPath); os.IsNotExist(err) {
		tojson.Save(t.friendsPath, friends)
	}
	err := tojson.Load(t.friendsPath, friends)
	if err != nil {
		return err
	}
	for _, v := range friends.Ids {
		v.Follow = false
	}
	for v := range t.twitterClient.GetFriendsIdsAll(nil) {
		for _, id := range v.Ids {
			strID := strconv.FormatInt(id, 10)
			user, ok := friends.Ids[strID]
			if ok {
				user.Follow = true
			} else {
				friends.Ids[strID] = &twitterUser{
					Timestamp: time.Now().UnixNano(),
					Follow:    true,
				}
			}
		}
	}
	err = tojson.Save(t.friendsPath, friends)
	if err != nil {
		return err
	}
	t.friends = friends
	return nil
}

// unfollowFriend flags the friend as not followed anymore.
// We do not remove friends from database, we just flag them as non friend.
func (t *TwitterBot) unfollowFriend(id int64) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.friends.Ids[strconv.FormatInt(id, 10)].Follow = false
	err := tojson.Save(t.friendsPath, t.friends)
	if err != nil {
		log.Fatalln(err)
	}
}

func (t *TwitterBot) getFriendToUnFollow() (int64, bool) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for strID, user := range t.friends.Ids {
		// unfollow only if is followed and is in database from at least 1 day
		if time.Now().UnixNano()-user.Timestamp < oneDayInNano || !user.Follow {
			continue
		}
		id, err := strconv.ParseInt(strID, 10, 64)
		if err != nil {
			log.Fatalln(err)
		}
		return id, true
	}
	return 0, false
}

func (t *TwitterBot) unfollowAll(sleepPolicy *SleepPolicy) {
	var id int64
	for ok := true; ok; id, ok = t.getFriendToUnFollow() {
		if !ok {
			break
		}
		user, err := t.twitterClient.UnfollowUserId(id)
		if err != nil {
			checkBotRestriction(err)
			continue
		}
		t.unfollowFriend(id)
		log.Printf("[twitter] unfollowing (id:%d, name:%s)\n", user.Id, user.Name)
		t.controlledSleep(sleepPolicy)
	}
	log.Println("[twitter] no more friends to unfollow, waiting 3 hours...")
	time.Sleep(3 * time.Hour)
	t.unfollowAll(sleepPolicy)
}

func (t *TwitterBot) isFollower(id int64) bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	_, ok := t.followers.Ids[strconv.FormatInt(id, 10)]
	return ok
}

func (t *TwitterBot) getFriend(id int64) (*twitterUser, bool) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	user, ok := t.friends.Ids[strconv.FormatInt(id, 10)]
	if ok {
		return &twitterUser{
			Timestamp: user.Timestamp,
			Follow:    user.Follow,
		}, ok
	}
	return nil, false
}

func (t *TwitterBot) addFriend(id int64) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.friends.Ids[strconv.FormatInt(id, 10)] = &twitterUser{
		Timestamp: time.Now().UnixNano(),
		Follow:    true,
	}
	err := tojson.Save(t.friendsPath, t.friends)
	if err != nil {
		log.Fatalln(err)
	}
}

func (t *TwitterBot) followAll(ids []int64, sleepPolicy *SleepPolicy) {
	for _, id := range ids {
		if _, ok := t.getFriend(id); ok || t.isFollower(id) {
			continue
		}
		user, err := t.twitterClient.FollowUserId(id, nil)
		if err != nil && !checkUnableToFollowAtThisTime(err) {
			checkBotRestriction(err)
			print(t, fmt.Sprintf("[twitter] failed to follow user (id:%d, name:%s), error: %v\n", user.Id, user.Name, err))
			continue
		}
		t.addFriend(id)
		log.Printf("[twitter] following (id:%d, name:%s)\n", user.Id, user.Name)
		t.controlledSleep(sleepPolicy)
	}
}

func (t *TwitterBot) fetchUserIds(query string, maxPage int) []int64 {
	users, err := t.twitterClient.GetUserSearch(query, nil)
	if err != nil {
		log.Fatalln(err.Error())
	}
	ids := []int64{}
	if len(users) == 0 {
		return nil
	}
	// gettings followers of the first user found
	user := users[0]
	nextCursor := "-1"
	currentPage := 1
	for {
		v := url.Values{}
		if nextCursor != "-1" {
			v.Set("cursor", nextCursor)
		}
		cursor, err := t.twitterClient.GetFollowersUser(user.Id, nil)
		if err != nil {
			checkBotRestriction(err)
			continue
		}
		for _, v := range cursor.Ids {
			ids = append(ids, v)
		}
		if currentPage >= maxPage {
			break
		}
		currentPage++
		nextCursor = cursor.Next_cursor_str
		if nextCursor == "0" {
			break
		}
	}
	return ids
}
