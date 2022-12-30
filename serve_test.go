package tiddlybucket

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

type dummyTiddlerStore struct {
	tiddlersByTitle           map[string]Tiddler
	simulateBackingStoreError bool
	// pathToTitle     map[string]string
}

func (s *dummyTiddlerStore) ReadFile(path string) (io.ReadCloser, error) {
	// TODO?
	return nil, nil
}

func (s *dummyTiddlerStore) GetTiddler(title string) (Tiddler, error) {
	tid, ok := s.tiddlersByTitle[title]
	if !ok {
		return nil, fmt.Errorf("did not find Tiddler with title %s", title)
	}
	return tid, nil
}

func (s *dummyTiddlerStore) GetAllTiddlers() ([]Tiddler, error) {
	if s.simulateBackingStoreError {
		return nil, fmt.Errorf("GetAllTiddlers(): simulated error")
	}
	tids := make([]Tiddler, 0)
	for _, t := range s.tiddlersByTitle {
		tids = append(tids, t)
	}
	return tids, nil
}

func (s *dummyTiddlerStore) WriteTiddler(t Tiddler) error {
	s.tiddlersByTitle[t["title"].(string)] = t
	// TODO: path?!
	return nil
}

func (s *dummyTiddlerStore) DeleteTiddler(title string) error {
	if _, ok := s.tiddlersByTitle[title]; !ok {
		return fmt.Errorf("did not find Tiddler with title %s", title)
	}
	delete(s.tiddlersByTitle, title)
	return nil
}

func Test_handlerWithStore_favicon(t *testing.T) {
	faviconTid := getTestTiddlerJsonAsTid(t, "favicon.json")
	faviconTid["text"] = bytes.NewBufferString(faviconTid["text"].(string)).Bytes()
	store := &dummyTiddlerStore{
		tiddlersByTitle: map[string]Tiddler{
			faviconTid["title"].(string): faviconTid,
		},
	}
	type fields struct {
		Store        TiddlerStore
		faviconCache *bytes.Buffer
	}
	tests := []struct {
		name           string
		fields         fields
		want           []byte
		wantType       string
		wantStatusCode int
	}{
		{"good favicon, in cache",
			fields{store, bytes.NewBuffer(faviconTid["text"].([]byte))},
			faviconTid["text"].([]byte), "image/x-icon", http.StatusOK},
		{"good favicon, not in cache",
			fields{store, new(bytes.Buffer)},
			faviconTid["text"].([]byte), "image/x-icon", http.StatusOK},
		{"missing favicon",
			fields{&dummyTiddlerStore{}, new(bytes.Buffer)},
			nil, "text/plain", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &handlerWithStore{
				Store:        tt.fields.Store,
				faviconCache: tt.fields.faviconCache,
			}
			r := httptest.NewRequest(http.MethodGet, "http://foobar.com/favicon.ico", nil)
			w := httptest.NewRecorder()
			h.favicon(w, r)
			resp := w.Result()
			if resp.StatusCode != tt.wantStatusCode {
				t.Errorf("favicon() unexpected status code = %d, want %d", resp.StatusCode, tt.wantStatusCode)
			}
			if !strings.Contains(resp.Header.Get("Content-Type"), tt.wantType) {
				t.Errorf("favicon() unexpected Content-Type = %s, want %s", resp.Header.Get("Content-Type"), tt.wantType)
			}
			if resp.StatusCode == http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("favicon() could not read server response = %v", err)
				}
				if string(body) != string(tt.want) {
					t.Errorf("favicon() did not receive expected response = %s, want %s", string(body), tt.want)
				}
			}
		})
	}
}

func Test_handlerWithStore_loginBasic(t *testing.T) {
	tests := []struct {
		name           string
		auth           authContext
		wantStatusCode int
	}{
		{"no username",
			authContext{Username: ""}, http.StatusUnauthorized},
		{"with username",
			authContext{Username: "random"}, http.StatusFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://foobar.com/login-basic", nil).
				WithContext(context.WithValue(context.Background(), "auth", tt.auth))
			w := httptest.NewRecorder()
			h := &handlerWithStore{}
			h.loginBasic(w, r)
			resp := w.Result()
			if resp.StatusCode != tt.wantStatusCode {
				t.Errorf("loginBasic() unexpected status code = %d, want %d", resp.StatusCode, tt.wantStatusCode)
			}
		})
	}
}

func TestCredentials_userCanWrite(t *testing.T) {
	type fields struct {
		UserPasswordsClearText map[string]string
		Readers                []string
		Writers                []string
	}
	type args struct {
		user            string
		isAuthenticated bool
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{"valid username, read:anon write:authorized",
			fields{
				map[string]string{"bobValid": "dilaVbob"},
				nil,
				[]string{"bobValid"}},
			args{"bobValid", true},
			true},
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Credentials{
				UserPasswordsClearText: tt.fields.UserPasswordsClearText,
				Readers:                tt.fields.Readers,
				Writers:                tt.fields.Writers,
			}
			if got := c.userCanWrite(tt.args.user, tt.args.isAuthenticated); got != tt.want {
				t.Errorf("Credentials.userCanWrite() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_basicAuthCtx(t *testing.T) {
	type args struct {
		creds                     Credentials
		username, password, route string
	}
	tests := []struct {
		name   string
		args   args
		want   authContext
		wantOK bool
	}{
		{"valid username, read:anon write:authorized",
			args{
				Credentials{
					map[string]string{"bobValid": "dilaVbob"},
					nil, []string{"bobValid"}},
				"bobValid", "dilaVbob", ""},
			authContext{
				"bobValid", false, true},
			true},
		{"missing basic auth, read:anon, write:named",
			args{
				Credentials{
					map[string]string{"bobValid": "dilaVbob"},
					nil, []string{"bobValid"}},
				"", "", ""},
			authContext{
				AuthAnonUsername, true, false},
			true},
		{"missing basic auth, read:named, write:none",
			args{
				Credentials{
					map[string]string{"bobValid": "dilaVbob"},
					[]string{"bobValid"}, nil},
				"", "", ""},
			authContext{},
			false},
		{"missing username, read:anon write:authorized",
			args{
				Credentials{
					map[string]string{"bobValid": "dilaVbob"},
					nil, []string{"bobValid"}},
				"", "dilaVbob", ""},
			authContext{
				AuthAnonUsername, true, false},
			true},
		{"missing username, read:authorized write:authorized",
			args{
				Credentials{
					map[string]string{"bobValid": "dilaVbob"},
					[]string{"bobValid"}, []string{"bobValid"}},
				"", "dilaVbob", ""},
			authContext{},
			false},
		{"missing password, read:anon write:authorized",
			args{
				Credentials{
					map[string]string{"bobValid": "dilaVbob"},
					nil, []string{"bobValid"}},
				"bobValid", "", ""},
			authContext{
				AuthAnonUsername, true, false},
			true},
		{"missing password, read:authorized write:authorized",
			args{
				Credentials{
					map[string]string{"bobValid": "dilaVbob"},
					[]string{"bobValid"}, []string{"bobValid"}},
				"bobValid", "", ""},
			authContext{},
			false},
		{"empty credentials",
			args{
				Credentials{},
				"bobValid", "dilaVbob", ""},
			authContext{
				AuthAnonUsername, true, true},
			true},
		{"missing basic auth to login-basic, read:authorized write:authorized",
			args{
				Credentials{
					map[string]string{"bobValid": "dilaVbob"},
					[]string{"bobValid"}, []string{"bobValid"}},
				"", "", "login-basic"},
			authContext{
				AuthAnonUsername, false, false},
			true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://foobar.com/%s", tt.args.route), nil)
			if tt.args.username != "" && tt.args.password != "" {
				r.SetBasicAuth(tt.args.username, tt.args.password)
			}
			w := httptest.NewRecorder()
			gotCtx, ok := basicAuthCtx(w, r, tt.args.creds)
			if ok != tt.wantOK {
				t.Errorf("basicAuthCtx() unexpected ok = %t, want %t", ok, tt.wantOK)
			}
			if !reflect.DeepEqual(gotCtx, tt.want) {
				t.Errorf("basicAuthCtx() unexpected context returned = %v, want %v", gotCtx, tt.want)
			}
		})
	}
}

func Test_handlerWithStore_status(t *testing.T) {
	tests := []struct {
		name string
		auth authContext
	}{
		{"no username",
			authContext{"", true, true}},
		{"with username",
			authContext{"random", false, true}},
		{"no authContext",
			authContext{Username: "TESTDONOTADD"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://foobar.com/status", nil)
			if tt.auth.Username != "TESTDONOTADD" {
				r = r.WithContext(context.WithValue(context.Background(), "auth", tt.auth))
			} else {
				tt.auth.Username = ""
			}
			w := httptest.NewRecorder()
			h := &handlerWithStore{}
			h.status(w, r)
			resp := w.Result()
			var status map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
				t.Errorf("status() could not read server response = %v", err)
			}
			if status["username"] != tt.auth.Username {
				t.Errorf("status() did not receive expected username = %s, want = %s", status["username"], tt.auth.Username)
			}
			if status["anonymous"] != tt.auth.CanBeAnonymous {
				t.Errorf("status() did not receive expected anonymous value = %t, want = %t", status["anonymous"], tt.auth.CanBeAnonymous)
			}
			if status["read_only"] != !tt.auth.WritingAllowed {
				t.Errorf("status() did not receive expected read_only value = %t, want = %t", status["read_only"], !tt.auth.WritingAllowed)
			}

			for _, f := range []string{"space", "tiddlywiki_version"} {
				if _, ok := status[f]; !ok {
					t.Errorf("status() did not identify '%s' field in response", f)
				}
			}
		})
	}
}

type chiContextKey struct {
	name string
}

func (k *chiContextKey) String() string {
	return "chi context value " + k.name
}

func Test_handlerWithStore_getTiddler(t *testing.T) {
	dummy := getTestTiddlerJsonAsTid(t, "TestTiddler.json")
	store := &dummyTiddlerStore{
		tiddlersByTitle: map[string]Tiddler{
			"TestTiddler": getTestTiddlerJsonAsTid(t, "TestTiddler.json"),
		},
	}
	type args struct {
		recipe, tiddlerName string
	}
	tests := []struct {
		name           string
		args           args
		store          TiddlerStore
		want           Tiddler
		wantStatusCode int
	}{
		{"existing tiddler",
			args{"default", "TestTiddler"}, store,
			dummy, http.StatusOK},
		{"missing tiddler",
			args{"default", "NotATiddler"}, store,
			nil, http.StatusNotFound},
		{"missing tiddler name in route",
			args{"default", ""}, store,
			nil, http.StatusBadRequest},
		// TODO: add more test cases
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &handlerWithStore{
				Store: tt.store,
			}
			r := httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("http://foobar.com/recipes/%s/tiddlers/%s",
					tt.args.recipe, tt.args.tiddlerName), nil)
			r = r.WithContext(context.WithValue(r.Context(),
				chi.RouteCtxKey,
				&chi.Context{
					URLParams: chi.RouteParams{
						Keys:   []string{"recipe", "*"},
						Values: []string{tt.args.recipe, tt.args.tiddlerName},
					},
				}))
			w := httptest.NewRecorder()
			h.getTiddler(w, r)

			resp := w.Result()
			if resp.StatusCode != tt.wantStatusCode {
				t.Errorf("getTiddler() unexpected status code = %d, want %d", resp.StatusCode, tt.wantStatusCode)
			}

			if resp.StatusCode == http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("getTiddler() could not read server response = %v", err)
				}
				gotTid := convertJsonToTid(t, body)
				if !areTiddlersEqual(t, tt.want, gotTid) {
					t.Errorf("getTiddler() returned tiddler does not match expected")
				}
			}

		})
	}
}

func Test_handlerWithStore_putTiddler(t *testing.T) {
	dummy := getTestTiddlerJson(t, "TestTiddler.json")
	dummyAsTid := getTestTiddlerJsonAsTid(t, "TestTiddler.json")
	store := &dummyTiddlerStore{
		tiddlersByTitle: make(map[string]Tiddler),
	}
	type args struct {
		recipe, tiddlerName string
		tiddler             []byte
	}
	tests := []struct {
		name           string
		args           args
		store          dummyTiddlerStore
		wantIsUpdated  bool
		wantStatusCode int
	}{
		{"standard tiddler",
			args{"default", dummyAsTid["title"].(string), dummy},
			*store, false, http.StatusNoContent},
		// TODO: update existing tiddler
		// TODO: add more test cases
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonTid := convertJsonToTid(t, tt.args.tiddler)
			_, inStore := tt.store.tiddlersByTitle[jsonTid["title"].(string)]
			if !tt.wantIsUpdated && inStore {
				t.Error("found tiddler in store unexpectedly")
			}
			h := &handlerWithStore{
				Store: &tt.store,
			}
			r := httptest.NewRequest(http.MethodPut,
				fmt.Sprintf("http://foobar.com/recipes/%s/tiddlers/%s",
					tt.args.recipe, tt.args.tiddlerName),
				bytes.NewReader(tt.args.tiddler))
			r = r.WithContext(context.WithValue(r.Context(),
				chi.RouteCtxKey,
				&chi.Context{
					URLParams: chi.RouteParams{
						Keys:   []string{"recipe", "*"},
						Values: []string{tt.args.recipe, tt.args.tiddlerName},
					},
				}))
			w := httptest.NewRecorder()
			h.putTiddler(w, r)

			resp := w.Result()
			if resp.StatusCode != tt.wantStatusCode {
				t.Errorf("putTiddler() unexpected status code = %d, want %d", resp.StatusCode, tt.wantStatusCode)
			}

			gotTid, inStore := tt.store.tiddlersByTitle[jsonTid["title"].(string)]
			if !inStore {
				t.Errorf("putTiddler() tiddler not found in store with title %s", jsonTid["title"].(string))
			}
			if !areTiddlersEqual(t, jsonTid, gotTid) {
				t.Errorf("putTiddler() returned tiddler does not match expected")
			}
		})
	}
}

func Test_handlerWithStore_deleteTiddler(t *testing.T) {
	dummyAsTid := getTestTiddlerJsonAsTid(t, "TestTiddler.json")
	store := &dummyTiddlerStore{
		tiddlersByTitle: map[string]Tiddler{
			dummyAsTid["title"].(string): dummyAsTid,
		},
	}
	type args struct {
		bag, tiddlerName string
	}
	tests := []struct {
		name           string
		args           args
		store          dummyTiddlerStore
		wantDeleted    bool
		wantStatusCode int
	}{
		{"delete existing",
			args{"default", dummyAsTid["title"].(string)},
			*store, true, http.StatusOK},
		// TODO: add more test cases
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &handlerWithStore{
				Store: &tt.store,
			}
			r := httptest.NewRequest(http.MethodDelete,
				fmt.Sprintf("http://foobar.com/bags/%s/tiddlers/%s",
					tt.args.bag, tt.args.tiddlerName), nil)
			r = r.WithContext(context.WithValue(r.Context(),
				chi.RouteCtxKey,
				&chi.Context{
					URLParams: chi.RouteParams{
						Keys:   []string{"bag", "*"},
						Values: []string{tt.args.bag, tt.args.tiddlerName},
					},
				}))
			w := httptest.NewRecorder()
			h.deleteTiddler(w, r)

			resp := w.Result()
			if resp.StatusCode != tt.wantStatusCode {
				t.Errorf("deleteTiddler() unexpected status code = %d, want %d", resp.StatusCode, tt.wantStatusCode)
			}

			if _, gotNotDeleted := tt.store.tiddlersByTitle[tt.args.tiddlerName]; tt.wantDeleted && gotNotDeleted {
				t.Errorf("deleteTiddler() tiddler unexpectedly found still in the store")
			}
		})
	}
}

func Test_handlerWithStore_getSkinnyTiddlerList(t *testing.T) {
	stripText := func(tid Tiddler) Tiddler {
		skinnyTid := make(Tiddler)
		for k, v := range tid {
			if k == "text" {
				continue
			}
			skinnyTid[k] = v
		}
		return skinnyTid
	}
	store := &dummyTiddlerStore{
		tiddlersByTitle: map[string]Tiddler{
			"TestTiddler":    getTestTiddlerJsonAsTid(t, "TestTiddler.json"),
			"$:/favicon.ico": getTestTiddlerJsonAsTid(t, "favicon.json"),
			"another":        getTestTiddlerJsonAsTid(t, "another.json"),
		},
	}
	cache := []Tiddler{
		stripText(store.tiddlersByTitle["TestTiddler.json"]),
		// stripText(store.tiddlersByTitle["favicon.json"]),
		stripText(store.tiddlersByTitle["another.json"]),
	}
	emptyStore := &dummyTiddlerStore{tiddlersByTitle: make(map[string]Tiddler)}
	type fields struct {
		store           dummyTiddlerStore
		skinnyListCache []Tiddler
	}
	type args struct {
		recipe string
	}
	tests := []struct {
		name           string
		fields         fields
		args           args
		want           []Tiddler
		wantStatusCode int
	}{
		{"use cache",
			fields{*emptyStore, cache},
			args{"default"},
			cache, http.StatusOK},
		{"build cache",
			fields{*store, nil},
			args{"default"},
			cache, http.StatusOK},
		{"empty store",
			fields{*emptyStore, nil},
			args{"default"},
			[]Tiddler{}, http.StatusOK},
		{"nil store",
			fields{dummyTiddlerStore{simulateBackingStoreError: true}, nil},
			args{"default"},
			nil, http.StatusInternalServerError},
		// TODO: add more test cases
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &handlerWithStore{
				Store:           &tt.fields.store,
				skinnyListCache: tt.fields.skinnyListCache,
			}
			r := httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("http://foobar.com/recipes/%s/tiddlers.json",
					tt.args.recipe), nil)
			r = r.WithContext(context.WithValue(r.Context(),
				chi.RouteCtxKey,
				&chi.Context{
					URLParams: chi.RouteParams{
						Keys:   []string{"recipe"},
						Values: []string{tt.args.recipe},
					},
				}))
			w := httptest.NewRecorder()
			h.getSkinnyTiddlerList(w, r)

			resp := w.Result()
			if resp.StatusCode != tt.wantStatusCode {
				t.Errorf("getSkinnyTiddlerList() unexpected status code = %d, want %d", resp.StatusCode, tt.wantStatusCode)
			}
			if resp.StatusCode == http.StatusOK {
				var got []Tiddler
				if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
					t.Errorf("getSkinnyTiddlerList() could not read server response = %v", err)
				}
				if !areTiddlerSlicesEqual(t, tt.want, got) {
					t.Errorf("getSkinnyTiddlerList(): returned tiddler slice does not match expected")
				}
			}
		})
	}
}

func Test_handlerWithStore_index(t *testing.T) {
	store := &dummyTiddlerStore{
		tiddlersByTitle: map[string]Tiddler{
			"TestTiddler":    getTestTiddlerJsonAsTid(t, "TestTiddler.json"),
			"loading-splash": getTestTiddlerJsonAsTid(t, "loading-splash.json"),
			// "$:/favicon.ico": getTestTiddlerJsonAsTid(t, "favicon.json"),
			// "another":        getTestTiddlerJsonAsTid(t, "another.json"),
		},
	}
	type fields struct {
		Store      dummyTiddlerStore
		indexCache *bytes.Buffer
	}
	tests := []struct {
		name           string
		fields         fields
		wantStatusCode int
	}{
		{"build the cache",
			fields{*store, nil}, http.StatusOK},
		// TODO: add more test cases
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &handlerWithStore{
				Store: &tt.fields.Store,
				// indexCache: tt.fields.indexCache,
			}
			r := httptest.NewRequest(http.MethodGet, "http://foobar.com/index", nil)
			w := httptest.NewRecorder()
			h.index(w, r)

			resp := w.Result()
			if resp.StatusCode != tt.wantStatusCode {
				t.Errorf("getSkinnyTiddlerList() unexpected status code = %d, want %d", resp.StatusCode, tt.wantStatusCode)
			}
			if resp.StatusCode == http.StatusOK {
				reader := bufio.NewReader(resp.Body)
				for {
					line, err := reader.ReadString('\n')
					if err != nil && err != io.EOF {
						t.Logf("index line: %s", line)
						t.Fatalf("could not read line in index file: %v", err)
					}
					// t.Logf("line: %s", line)
					if strings.Contains(line, "<!--~~ Ordinary tiddlers ~~-->") {
						for {
							scriptLine, err := reader.ReadString('\n')
							if err == io.EOF {
								t.Fatalf("unexpectedly found EOF while trying to read tiddlers")
							}
							if strings.Contains(scriptLine, "</script>") {
								break
							}
							if strings.Contains(scriptLine, `<script class="tiddlywiki-tiddler-store"`) {
								continue
							}
							// t.Logf("script thingy:\n%s", strings.TrimSpace(scriptLine))
							var stored []Tiddler
							if err := json.NewDecoder(strings.NewReader(scriptLine)).Decode(&stored); err != nil {
								t.Errorf("index() could not read server response = %v", err)
							}
							want := make([]Tiddler, 0)
							for _, t := range tt.fields.Store.tiddlersByTitle {
								want = append(want, t)
							}
							if !areTiddlerSlicesEqual(t, want, stored) {
								t.Errorf("index(): returned tiddler slice does not match expected")
							}
						}
					} else if strings.Contains(line, "<!--~~ Raw markup for the top of the head section ~~-->") {
						var markup bytes.Buffer
						for {
							markupLine, err := reader.ReadString('\n')
							if err == io.EOF {
								t.Fatalf("unexpectedly found EOF while trying to read tiddlers")
							}
							if strings.Contains(markupLine, `<meta http-equiv="X-UA-Compatible" content="IE=Edge"/>`) {
								break
							}
							markup.WriteString(markupLine)
						}
						// TODO: check that this is correct
						t.Logf("head markup:\n%s", markup.String())
					} else if strings.Contains(line, "<!--~~ Raw markup for the top of the body section ~~-->") {
						var markup bytes.Buffer
						for {
							markupLine, err := reader.ReadString('\n')
							if err == io.EOF {
								t.Fatalf("unexpectedly found EOF while trying to read tiddlers")
							}
							if strings.Contains(markupLine, "<!--~~ Static styles ~~-->") {
								break
							}
							markup.WriteString(markupLine)
						}
						// TODO: check that this is correct
						t.Logf("body-top markup:\n%s", markup.String())
					} else if strings.Contains(line, "<!--~~ Raw markup for the bottom of the body section ~~-->") {
						var markup bytes.Buffer
						for {
							markupLine, err := reader.ReadString('\n')
							if err == io.EOF {
								t.Fatalf("unexpectedly found EOF while trying to read tiddlers")
							}
							if strings.Contains(markupLine, `</body>`) {
								break
							}
							markup.WriteString(markupLine)
						}
						// TODO: check that this is correct
						t.Logf("body-bottom markup:\n%s", markup.String())
					}
					if err == io.EOF {
						break
					}
				}
			}
		})
	}
}

func Test_handlerWithStore_resetCaches(t *testing.T) {
	type fields struct {
		indexCache      *bytes.Buffer
		faviconCache    *bytes.Buffer
		skinnyListCache []Tiddler
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{"basic test", fields{
			bytes.NewBufferString("this is dummy text"),
			bytes.NewBufferString("my favorite icon"),
			[]Tiddler{getTestTiddlerJsonAsTid(t, "TestTiddler.json")}}},
		{"missing caches", fields{nil, nil, nil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &handlerWithStore{
				indexCache:      tt.fields.indexCache,
				faviconCache:    tt.fields.faviconCache,
				skinnyListCache: tt.fields.skinnyListCache,
			}
			h.resetCaches()

			if h.indexCache != nil && h.indexCache.Len() > 0 {
				t.Errorf("handlerWithStore.resetCaches() unexpectedly found indexCache not empty %d", h.indexCache.Len())
			}
			if h.faviconCache != nil && h.faviconCache.Len() > 0 {
				t.Errorf("handlerWithStore.resetCaches() unexpectedly found faviconCache not empty %d", h.faviconCache.Len())
			}
			if len(h.skinnyListCache) > 0 {
				t.Errorf("handlerWithStore.resetCaches() unexpectedly found skinnyListCache not empty %d", len(h.skinnyListCache))
			}
		})
	}
}
