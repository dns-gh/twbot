package twbot

import (
	"testing"

	. "gopkg.in/check.v1"
)

func Test(t *testing.T) { TestingT(t) }

type MySuite struct{}

var _ = Suite(&MySuite{})

// go test ...twbot -gocheck.vv -test.v -gocheck.f TestTruncate
const (
	string141 = "Wrote water woman of heart it total other. By in entirely securing suitable graceful at families improved. Zealously few furniture repulsive."
	string140 = "Wrote water woman of heart it total other. By in entirely securing suitable graceful at families improved. Zealously few furniture repulsive"
	string42  = "Wrote water woman of heart it total other."
	url140    = "https://------------------------------------------" +
		"--------------------------------------------------" +
		"----------------------------------------"
)

// twitter messages are 140 char long maximum, so we check here
// several displays when you got a message and an url to deal with.
func (s *MySuite) TestTruncate(c *C) {
	trunc := truncate("test", "")
	c.Assert(trunc, Equals, "test")
	trunc = truncate("test", "test_url")
	c.Assert(trunc, Equals, "test test_url")
	trunc = truncate("test sentence with at least 30 characters", "test_url_long_enough________________________________________________________________________________")
	c.Assert(trunc, Equals, "test sentence with at least 30 chara... test_url_long_enough________________________________________________________________________________")
	trunc = truncate(string42, url140)
	c.Assert(trunc, Equals, url140)
	trunc = truncate(string141, "")
	c.Assert(trunc, Equals, "Wrote water woman of heart it total other. "+
		"By in entirely securing suitable graceful at families improved. Zealously few furniture repul...")
	trunc = truncate(string140, "")
	c.Assert(trunc, Equals, string140)
}
