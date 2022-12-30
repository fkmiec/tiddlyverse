package tiddlybucket

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thanhpk/randstr"
)

var (
	emptyTiddlerFile = map[string]interface{}{"revision": "0"}
	testDataDir      string
)

func init() {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	testDataDir = filepath.Join(wd, "testdata")
}

func getTestTiddler(t *testing.T, name string) []byte {
	dummy, err := os.ReadFile(filepath.Join(testDataDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return dummy
}

func getTestTiddlerJson(t *testing.T, name string) []byte {
	dummyJson, err := os.ReadFile(filepath.Join(testDataDir, name))
	if err != nil {
		t.Fatal(err)
	}

	return dummyJson
}

func convertJsonToTid(t *testing.T, jsonTid []byte) Tiddler {
	var dummyAsTid Tiddler
	if err := json.NewDecoder(strings.NewReader(string(jsonTid))).Decode(&dummyAsTid); err != nil {
		t.Fatal(err)
	}

	/*
		// if tags, ok := dummyAsTid["tags"]; ok && len(tags.(string)) > 0 {
		if tags, ok := dummyAsTid["tags"]; ok {
			switch tags.(type) {
			case string:
				tagsStr := reMatchMultiWordTags.ReplaceAllStringFunc(tags.(string), func(str string) string { return strings.ReplaceAll(str, " ", "~tb~") })
				tagsArr := make([]interface{}, 0)
				for _, t := range strings.Split(tagsStr, " ") {
					tagsArr = append(tagsArr, strings.ReplaceAll(t, "~tb~", " "))
				}
				dummyAsTid["tags"] = tagsArr
			case []interface{}:
			default:
			}
		}
	*/

	return dummyAsTid
}

func getTestTiddlerJsonAsTid(t *testing.T, name string) Tiddler {
	return convertJsonToTid(t, getTestTiddlerJson(t, name))
}

func areTiddlersEqual(t *testing.T, want, have Tiddler) bool {
	_, haveRev := have["revision"]
	_, wantRev := want["revision"]
	if len(want) != len(have) && haveRev && wantRev {
		t.Logf("incorrect number of fields. want = %d, have = %d", len(want), len(have))
		return false
	}

	for k, v := range want {
		hv, ok := have[k]
		if !ok {
			t.Logf("expected field not found. want = %s", k)
			return false
		}
		var vIsArray, hvIsArray bool
		switch v.(type) {
		case []string, []interface{}:
			vIsArray = true
		}
		switch hv.(type) {
		case []string, []interface{}:
			hvIsArray = true
		}
		if k == "tags" {
			t.Logf("have: (%T,%t)%q\n", hv, hvIsArray, hv)
			// t.Logf("(%T,%t)%s, (%T,%t)%s\n", v, vIsArray, v, hv, hvIsArray, hv)
		}
		if vIsArray && !hvIsArray {
			t.Logf("expected field types do not match for %s: %T want = %T", k, hv, v)
			return false
		}
		if !vIsArray && !hvIsArray && v.(string) != hv.(string) {
			t.Logf("expected field values do not match for %s: %s want = %s", k, hv, v)
			return false
		}
		if vIsArray && hvIsArray {
			for _, w := range hv.([]interface{}) {
				var found bool
				for _, h := range v.([]interface{}) {
					if w.(string) == h.(string) {
						found = true
						break
					}
				}
				if !found {
					t.Logf("expected field values do not match for %s: %q want = %q", k, hv, v)
					return false
				}
			}
		}
	}

	return true
}

func areTiddlerSlicesEqual(t *testing.T, want, have []Tiddler) bool {
	for _, wantTid := range want {
		var found bool
		for _, haveTid := range have {
			if areTiddlersEqual(t, wantTid, haveTid) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func areTiddlerMapsEqual(t *testing.T, want, have map[string]Tiddler) bool {
	for wantTitle, wantTid := range want {
		haveTid, ok := have[wantTitle]
		if !ok || !areTiddlersEqual(t, wantTid, haveTid) {
			return false
		}
	}
	return true
}

func generateTiddler(t *testing.T) Tiddler {
	tid := make(Tiddler)
	rand.Seed(time.Now().UnixNano())

	rstr := func() string {
		return randstr.String(rand.Intn(30) + 5)
	}

	tid["title"] = rstr()
	tid["created"] = time.Now().Format("20060102150405000")
	tid["modified"] = time.Now().
		Add(time.Duration(rand.Intn(200)+55) + time.Minute).
		Format("20060102150405000")

	// tags
	tags := make([]string, rand.Intn(8)+2)
	multiWordPos := rand.Intn(len(tags)-1) + 1
	t.Logf("lt: %d, mw: %d\n", len(tags), multiWordPos)
	for i := range tags {
		s := rstr()
		if multiWordPos == i {
			s += " " + rstr()
		}
		tags[i] = s
	}
	t.Logf("gentags: %q\n", tags)
	tid["tags"] = strings.Join(tags, " ")

	// random fields
	i := len(rand.Perm(rand.Intn(10) + 2))
	for i > 0 {
		tid[rstr()] = rstr()
		i--
	}

	return tid
}
