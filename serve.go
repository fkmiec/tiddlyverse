package tiddlybucket

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/render"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	bag                    = "default"
	AuthAnonUsername       = "GUEST" // https://github.com/Jermolene/TiddlyWiki5/blob/master/plugins/tiddlywiki/tiddlyweb/tiddlywebadaptor.js#L91
	authTokenAuthenticated = "(authenticated)"
)

var serverHostAndPort string
var serverBasePath string
var serverStorageType string
var serverStorageLocation string
var serverTrashLocation string
var routeHandlers *handlerRouter

type Credentials struct {
	UserPasswordsClearText map[string]string
	Readers                []string
	Writers                []string
}

func (c Credentials) userCanWrite(user string, isAuthenticated bool) bool {
	if c.Writers == nil || len(c.Writers) == 0 {
		return true
	}

	for _, r := range c.Writers {
		if r == user && isAuthenticated {
			return true
		}
	}

	return false
}

type handlerRouter struct {
	handlerWithStoreMap map[string]*handlerWithStore
}

func (hr *handlerRouter) getWikiList() [][]string {
	var description string
	wikis := make([][]string, len(hr.handlerWithStoreMap))
	i := 0
	for name := range hr.handlerWithStoreMap {
		tid, err := hr.handlerWithStoreMap[name].Store.GetTiddler("$:/SiteDescription")
		if err != nil {
			description = "To include a description, add a tiddler titled $:/SiteDescription to the wiki"
		} else {
			description = tid.Field("text")
		}
		wikis[i] = make([]string, 2)
		wikis[i][0] = name
		wikis[i][1] = description
		i++
	}
	sort.SliceStable(wikis, func(i, j int) bool {
		return wikis[i][0] < wikis[j][0]
	})
	return wikis
}

func (hr *handlerRouter) getHandlerWithStore(wiki string) (*handlerWithStore, error) {
	//wiki := strings.Split(r.URL.Path, "/")[1]
	h, ok := hr.handlerWithStoreMap[wiki]
	if !ok {
		return nil, errors.New("No wiki found: " + wiki)
	}
	fmt.Println("handlerRouter.getHandlerWithStore. Wiki=", wiki, ", handler=", fmt.Sprintf("%T", h))
	return h, nil
}

func (hr *handlerRouter) addHandler(wiki string, handler *handlerWithStore) {
	hr.handlerWithStoreMap[wiki] = handler
}

func (hr *handlerRouter) index(w http.ResponseWriter, r *http.Request) {
	wiki := chi.URLParam(r, "wiki")
	h, err := hr.getHandlerWithStore(wiki)
	if err != nil {
		log.Warn().Err(err).Msg("Wiki not found: " + wiki)
		http.Redirect(w, r, "/", http.StatusInternalServerError)
		return
	}
	h.index(w, r)
}

func (hr *handlerRouter) favicon(w http.ResponseWriter, r *http.Request) {
	fmt.Println("FAVICON: ", r.URL.Path)
	wiki := chi.URLParam(r, "wiki")
	h, err := hr.getHandlerWithStore(wiki)
	if err != nil {
		log.Warn().Err(err).Msg("Wiki not found: " + wiki)
		http.Redirect(w, r, "/", http.StatusInternalServerError)
		return
	}
	h.favicon(w, r)
}

func (hr *handlerRouter) loginBasic(w http.ResponseWriter, r *http.Request) {
	wiki := chi.URLParam(r, "wiki")
	h, err := hr.getHandlerWithStore(wiki)
	if err != nil {
		log.Warn().Err(err).Msg("Wiki not found: " + wiki)
		http.Redirect(w, r, "/", http.StatusInternalServerError)
		return
	}
	h.loginBasic(w, r)
}

func (hr *handlerRouter) status(w http.ResponseWriter, r *http.Request) {
	wiki := chi.URLParam(r, "wiki")
	h, err := hr.getHandlerWithStore(wiki)
	if err != nil {
		log.Warn().Err(err).Msg("Wiki not found: " + wiki)
		http.Redirect(w, r, "/", http.StatusInternalServerError)
		return
	}
	h.status(w, r)
}

func (hr *handlerRouter) getSkinnyTiddlerList(w http.ResponseWriter, r *http.Request) {
	wiki := chi.URLParam(r, "wiki")
	h, err := hr.getHandlerWithStore(wiki)
	if err != nil {
		log.Warn().Err(err).Msg("Wiki not found: " + wiki)
		http.Redirect(w, r, "/", http.StatusInternalServerError)
		return
	}
	h.getSkinnyTiddlerList(w, r)
}

func (hr *handlerRouter) getTiddler(w http.ResponseWriter, r *http.Request) {
	wiki := chi.URLParam(r, "wiki")
	h, err := hr.getHandlerWithStore(wiki)
	if err != nil {
		log.Warn().Err(err).Msg("Wiki not found: " + wiki)
		http.Redirect(w, r, "/", http.StatusInternalServerError)
		return
	}
	h.getTiddler(w, r)
}

func (hr *handlerRouter) putTiddler(w http.ResponseWriter, r *http.Request) {
	wiki := chi.URLParam(r, "wiki")
	h, err := hr.getHandlerWithStore(wiki)
	if err != nil {
		log.Warn().Err(err).Msg("Wiki not found: " + wiki)
		http.Redirect(w, r, "/", http.StatusInternalServerError)
		return
	}
	h.putTiddler(w, r)
}

func (hr *handlerRouter) deleteTiddler(w http.ResponseWriter, r *http.Request) {
	wiki := chi.URLParam(r, "wiki")
	h, err := hr.getHandlerWithStore(wiki)
	if err != nil {
		log.Warn().Err(err).Msg("Wiki not found: " + wiki)
		http.Redirect(w, r, "/", http.StatusInternalServerError)
		return
	}
	//h := hr.getHandlerWithStore(wiki)
	h.deleteTiddler(w, r)
}

type handlerWithStore struct {
	Store                                           TiddlerStore
	indexCache, faviconCache                        *bytes.Buffer
	skinnyListCache                                 []Tiddler
	muSkinnyListCache, muIndexCache, muFaviconCache sync.RWMutex
}

func (h *handlerWithStore) setCustomPath(wikiName string) error {
	tid, err := h.Store.GetTiddler("$:/config/tiddlyweb/host")
	if err != nil {
		timestamp := time.Now().Format("20060102150405999")
		tid := Tiddler{}
		tid["created"] = timestamp
		tid["modified"] = timestamp
		tid["title"] = "$:/config/tiddlyweb/host"
		tid["text"] = "http://" + serverHostAndPort + "/" + wikiName + "/"
	} else {
		tid.setField("text", "http://"+serverHostAndPort+"/"+wikiName+"/")
	}
	if err := h.Store.WriteTiddler(tid); err != nil {
		log.Error().Err(err).Msg("Failed to write custom path tiddler in store.")
		return err
	}
	return nil
}

func (h *handlerWithStore) setIndexCache(b []byte) {
	h.muIndexCache.Lock()
	defer h.muIndexCache.Unlock()
	h.indexCache = bytes.NewBuffer(b)
}

func (h *handlerWithStore) getIndexCache() string {
	h.muIndexCache.RLock()
	defer h.muIndexCache.RUnlock()
	if h.indexCache == nil {
		return ""
	}
	return h.indexCache.String()
}

func (h *handlerWithStore) setFaviconCache(b []byte) {
	h.muFaviconCache.Lock()
	defer h.muFaviconCache.Unlock()
	h.faviconCache = bytes.NewBuffer(b)
}

func (h *handlerWithStore) getFaviconCache() []byte {
	h.muFaviconCache.RLock()
	defer h.muFaviconCache.RUnlock()
	if h.faviconCache == nil {
		return nil
	}
	return h.faviconCache.Bytes()
}

func (h *handlerWithStore) setSkinnyListCache(t []Tiddler) {
	h.muSkinnyListCache.Lock()
	defer h.muSkinnyListCache.Unlock()
	h.skinnyListCache = make([]Tiddler, 0)
	h.skinnyListCache = append(h.skinnyListCache, t...)
}

func (h *handlerWithStore) getSkinnyListCache() []Tiddler {
	h.muSkinnyListCache.RLock()
	defer h.muSkinnyListCache.RUnlock()
	return h.skinnyListCache
}

func (h *handlerWithStore) resetCaches() {
	if h.indexCache != nil {
		h.muIndexCache.Lock()
		defer h.muIndexCache.Unlock()
		h.indexCache.Reset()
	}

	if h.faviconCache != nil {
		h.muFaviconCache.Lock()
		defer h.muFaviconCache.Unlock()
		h.faviconCache.Reset()
	}

	if h.skinnyListCache != nil {
		h.muSkinnyListCache.Lock()
		defer h.muSkinnyListCache.Unlock()
		h.skinnyListCache = make([]Tiddler, 0)
	}
}

func serverRootIndex(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var pageBytes bytes.Buffer
	pageBytes.WriteString("<html>")
	pageBytes.WriteString("<head>")
	pageBytes.WriteString("<script>")
	//pageBytes.WriteString("function deleteWiki(wiki) {\nif(confirm(\"Are you sure you want to delete ' + wiki + '?\") == true) {\nwindow.location.href=\"deleteWiki?name=\" + wiki + \";\n}}")
	pageBytes.WriteString("function deleteWiki(wiki){if(confirm(\"Are you sure you want to delete \" + wiki + \"?\") == true) {window.location.href=\"deleteWiki?name=\" + wiki;}}")
	pageBytes.WriteString("</script>")
	pageBytes.WriteString("<style>table, th, td { border: 1px solid; border-collapse: collapse; } th, td { padding: 10px; text-align: left; } tr:hover {background-color: #FAFAD2;}</style>")
	pageBytes.WriteString("</head>")
	pageBytes.WriteString("<body>")
	pageBytes.WriteString("<h1>Welcome to your TiddlyWiki server</h1>")
	pageBytes.WriteString("<p>This server hosts one or more TiddlyWiki wikis. Below is a list of the current wikis.")
	pageBytes.WriteString("<p><table style='border:1'><tr><th>Wiki</th><th>Description</th><th>Action</th></tr>")
	wikis := routeHandlers.getWikiList()
	for _, wiki := range wikis {
		//TODO - Finish the delete logic
		pageBytes.WriteString("<tr><td><a href='" + wiki[0] + "')>" + wiki[0] + "</a></td><td>" + wiki[1] + "</td><td><a href='javascript:deleteWiki(\"" + wiki[0] + "\")'>Delete</a></td></tr>")
	}
	pageBytes.WriteString("</table>")
	pageBytes.WriteString("<p><a href=\"addWiki\">Click here to create a new wiki</a>")
	pageBytes.WriteString("</body>")
	pageBytes.WriteString("</html>")

	page := pageBytes.String()

	log.Info().
		Int("len", len(page)).
		Dur("ellapsed", time.Since(start)).
		Float64("ellapsed_min", time.Since(start).Minutes()).
		Msg("sending Server Root index")

	render.HTML(w, r, page)
}

func addWiki(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var pageBytes bytes.Buffer
	pageBytes.WriteString("<html>")
	pageBytes.WriteString("<head>")
	pageBytes.WriteString("<script>")
	pageBytes.WriteString("function createNewWiki(template) {\nvar name = document.getElementById('wikiname').value;\nwindow.location.href='createNewWiki?name=' + name + '&template=' + template;\n}")
	pageBytes.WriteString("</script>")
	pageBytes.WriteString("<style>table, th, td { border: 1px solid; border-collapse: collapse; } th, td { padding: 10px; text-align: left; } tr:hover {background-color: #FAFAD2;}</style>")
	pageBytes.WriteString("</head>")
	pageBytes.WriteString("<body>")
	pageBytes.WriteString("<h1>Create New TiddlyWiki</h1>")
	pageBytes.WriteString("<p>Confirm or update the name of the new wiki and then click the chosen template for the wiki. Clicking the template link will initiate creation and redirect you to your new wiki.")
	pageBytes.WriteString("<p><span><b>Wiki Name: </b></span><input type='text' id='wikiname' value='MyNewWiki'/>")
	pageBytes.WriteString("<p><table style='border:1'><tr><th>Template</th><th>Description</th></tr>")
	templates, err := getWikiTemplates(filepath.Join(serverBasePath, "templates"))
	if err != nil {
		log.Warn().Err(err).Msg("Error processing templates for the addWiki page.")
		http.Redirect(w, r, "/", http.StatusInternalServerError)
		return
	}
	for _, template := range templates {
		pageBytes.WriteString("<tr><td><a href='javascript:createNewWiki(\"" + template[1] + "\")'>" + template[0] + "</a></td><td>" + template[2] + "</td></tr>")
	}
	pageBytes.WriteString("</table>")
	pageBytes.WriteString("<p><p>New template wikis can be added to the templates folder on the server.")
	pageBytes.WriteString("<p>Each template is a TiddlyWiki .html file with a unique name (ie. not index.html), together with a .txt file with the same name and a short description.")
	pageBytes.WriteString("<p><p><a href=\"/\">Home</a>")
	pageBytes.WriteString("</body>")
	pageBytes.WriteString("</html>")

	page := pageBytes.String()

	log.Info().
		Int("len", len(page)).
		Dur("ellapsed", time.Since(start)).
		Float64("ellapsed_min", time.Since(start).Minutes()).
		Msg("sending addWiki")

	render.HTML(w, r, page)
}

/*
Create a new wiki with provided name and template params
- Validate that the template name exists. If not exist, redirect to serverRootIndex
- Create a new directory under wiki folder with the provided name
- Copy the designated template file to the new directory and rename as index.html
- Create a tiddlers folder in the new directory
- Write the system tiddler $:/config/tiddlyweb/host with the value http://<server host/port>/<wiki folder>/<new wiki name> into tiddlers folder.
- Redirect to the new wiki
*/
func createNewWiki(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	wikiName := r.URL.Query().Get("name")
	templateFilename := r.URL.Query().Get("template")
	wikiPath := filepath.Join(serverStorageLocation, wikiName)
	templateFilePath := filepath.Join(serverBasePath, "templates", templateFilename)
	createWikiFolder(wikiPath, templateFilePath)

	store, err := NewFileStore(wikiPath)
	if err != nil {
		log.Error().Err(err).Msg("Unable to create new wiki. Failed to create new store.")
		http.Error(w, fmt.Sprintf("Unable to create new wiki. Failed to create new store: %s", err.Error()), http.StatusInternalServerError)
		return
	}
	handler := &handlerWithStore{Store: store}
	//fmt.Println("Adding handler: ", wikiName, " = ", fmt.Sprintf("%T", handler))
	routeHandlers.addHandler(wikiName, handler)

	//Enable custom path so TiddlyWiki doesn't request files relative to server root, but rather relative to this new wiki folder
	//Write the system tiddler $:/config/tiddlyweb/host with the value http://<server host/port>/<wiki folder>/<new wiki name> into tiddlers folder.
	handler.setCustomPath(wikiName)
	/*
		timestamp := start.Format("20060102150405999")
		newTiddler := Tiddler{}
		newTiddler["created"] = timestamp
		newTiddler["modified"] = timestamp
		newTiddler["title"] = "$:/config/tiddlyweb/host"
		newTiddler["text"] = "http://" + serverHostAndPort + "/" + wikiName + "/"

		if err := handler.Store.WriteTiddler(newTiddler); err != nil {
			log.Error().Err(err).Msg("Unable to create new wiki. Failed to write custom path tiddler to store.")
			http.Error(w, fmt.Sprintf("Unable to create new wiki. Failed to write custom path tiddler to store: %s", err.Error()), http.StatusInternalServerError)
			return
		}
	*/
	log.Info().
		Dur("ellapsed", time.Since(start)).
		Float64("ellapsed_min", time.Since(start).Minutes()).
		Msg("sending createNewWiki")

	http.Redirect(w, r, "/"+wikiName, http.StatusFound)
}

func deleteWiki(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	wikiName := r.URL.Query().Get("name")
	wikiPath := filepath.Join(serverStorageLocation, wikiName)
	copyFolder(wikiPath, filepath.Join(serverTrashLocation, wikiPath)) //FIX THIS - This copies entire root directory structure (but not all the files) from / to the intended wiki folder.
	deleteFolder(wikiPath)
	delete(routeHandlers.handlerWithStoreMap, wikiName)
	log.Info().
		Dur("ellapsed", time.Since(start)).
		Float64("ellapsed_min", time.Since(start).Minutes()).
		Msg("sending deleteWiki")

	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *handlerWithStore) index(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	page := h.getIndexCache()
	log.Trace().Int("len", len(page)).Str("page", page).Msg("retrieved index.html from cache")
	if len(page) <= 0 {
		var pageBytes bytes.Buffer
		log.Trace().Msg("creating index cache")

		// Grab the tiddlers and clean them up
		tids, err := h.Store.GetAllTiddlers()
		if err != nil {
			log.Error().Err(err).Msg("could not read tiddler from store")
			http.Error(w, fmt.Sprintf("could not read tiddlers from store: %s", err.Error()), http.StatusInternalServerError)
			return
		}
		rawMarkupTiddlers := make(map[string][]Tiddler)
		rawMarkupTiddlers["head"] = make([]Tiddler, 0)
		rawMarkupTiddlers["body-top"] = make([]Tiddler, 0)
		rawMarkupTiddlers["body-bottom"] = make([]Tiddler, 0)
		for i, tid := range tids {
			// This is here because the TiddlyWeb plugin will not issue a DELETE request if the tiddler is not in a bag
			// https://github.com/Jermolene/TiddlyWiki5/blob/master/plugins/tiddlywiki/tiddlyweb/tiddlywebadaptor.js#L250-L253
			if _, ok := tid["bag"]; !ok {
				tids[i]["bag"] = bag
			}
			// TiddlyWeb format is not expected in this store
			log.Trace().Interface("tid", tid).Msg("checking to see if it is a rawmarkup tiddler")
			if tagsRaw, ok := tid["tags"]; ok {
				switch tagsRaw.(type) {
				case []interface{}:
					tags := make([]string, len(tagsRaw.([]interface{})))
					for j, t := range tagsRaw.([]interface{}) {
						tags[j] = t.(string)
					}
					tids[i]["tags"] = strings.Join(tags, " ")
				case []string:
					tids[i]["tags"] = strings.Join(tagsRaw.([]string), " ")
				case string, interface{}: // assume it's a string already
				default:
					log.Fatal().Str("title", tid["title"].(string)).Str("tags_type", fmt.Sprintf("%T", tagsRaw)).Interface("tagsRaw", tagsRaw).Msg("unexpected type for tags field")
				}
				if strings.Contains(tids[i]["tags"].(string), "$:/tags/RawMarkup") && tids[i]["text"] != nil {
					if strings.Contains(tid["tags"].(string), "/TopBody") {
						rawMarkupTiddlers["body-top"] = append(rawMarkupTiddlers["body-top"], tid)
					} else if strings.Contains(tid["tags"].(string), "/BottomBody") {
						rawMarkupTiddlers["body-bottom"] = append(rawMarkupTiddlers["body-bottom"], tid)
					} else {
						rawMarkupTiddlers["head"] = append(rawMarkupTiddlers["head"], tid)
					}
				}
			}
		}
		log.Trace().Interface("rawMarkupTiddlers", rawMarkupTiddlers).Send()

		// Read in the index file and include the tiddlers into the store
		indexReader, err := h.Store.ReadFile("index.html")
		if err != nil {
			log.Fatal().Err(err).Msg("can't open the index file!")
		}
		reader := bufio.NewReader(indexReader)
		for {
			line, err := reader.ReadString('\n')
			if err != nil && err != io.EOF {
				log.Fatal().Str("line", line).Err(err).Msg("could not read line in index file")
			}
			pageBytes.WriteString(line)
			if strings.Contains(line, "<!--~~ Ordinary tiddlers ~~-->") {
				pageBytes.WriteString(`<script class="tiddlywiki-tiddler-store" type="application/json">` + "\n")
				var b bytes.Buffer
				if err := json.NewEncoder(&b).Encode(tids); err != nil {
					log.Fatal().Interface("tids", tids).Err(err).Msg("could not encode tiddlers into json for index")
				}
				pageBytes.WriteString(strings.ReplaceAll(b.String(), "<", "\u003c"))
				// pageBytes.WriteString(strings.ReplaceAll(strings.ReplaceAll(b.String(), "<", "\u003c"), "},{", "},\n{"))
				pageBytes.WriteString("</script>\n")
			} else if strings.Contains(line, "<!--~~ Raw markup for the top of the head section ~~-->") {
				for _, tid := range rawMarkupTiddlers["head"] {
					pageBytes.WriteString(tid["text"].(string))
				}
			} else if strings.Contains(line, "<!--~~ Raw markup for the top of the body section ~~-->") {
				for _, tid := range rawMarkupTiddlers["body-top"] {
					pageBytes.WriteString(tid["text"].(string))
				}
			} else if strings.Contains(line, "<!--~~ Raw markup for the bottom of the body section ~~-->") {
				for _, tid := range rawMarkupTiddlers["body-bottom"] {
					pageBytes.WriteString(tid["text"].(string))
				}
			}
			if err == io.EOF {
				break
			}
		}

		page = pageBytes.String()
		h.setIndexCache([]byte(page))
	}

	log.Info().
		Int("len", len(page)).
		Dur("ellapsed", time.Since(start)).
		Float64("ellapsed_min", time.Since(start).Minutes()).
		Msg("sending index")

	render.HTML(w, r, page)
}

func (h *handlerWithStore) favicon(w http.ResponseWriter, r *http.Request) {
	icon := h.getFaviconCache()

	if len(icon) == 0 {
		tid, err := h.Store.GetTiddler("$:/favicon.ico")
		if err != nil {
			log.Warn().Err(err).Msg("could not find $:/favicon.ico")
			http.Error(w, fmt.Sprintf("could not find $:/favicon.ico: %s", err.Error()), http.StatusNotFound)
			return
		}
		var buf bytes.Buffer
		buf.Write(tid["text"].([]byte))
		icon = buf.Bytes()
		h.setFaviconCache(icon)
	}

	w.Header().Set("Content-Type", "image/x-icon")
	w.Write(icon)
}

func (h *handlerWithStore) loginBasic(w http.ResponseWriter, r *http.Request) {
	auth, ok := r.Context().Value("auth").(authContext)
	log.Trace().Interface("auth", auth).Bool("ok", ok).Msg("checking logged in user?")
	if auth.Username == "" || auth.Username == AuthAnonUsername {
		w.Header().Set("WWW-Authenticate", `Basic realm="Please provide your username and password to login"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	log.Info().Str("username", auth.Username).Msg("successfully logged in")
	http.Redirect(w, r, "/", http.StatusFound) // TODO: server prefix
}

func (h *handlerWithStore) status(w http.ResponseWriter, r *http.Request) {
	auth, ok := r.Context().Value("auth").(authContext)
	log.Trace().Interface("auth", auth).Bool("ok", ok).Msg("found creds")
	if !ok {
		log.Warn().Msg("no auth context found!")
	}
	render.JSON(w, r, map[string]interface{}{
		"username":             auth.Username,
		"anonymous":            auth.CanBeAnonymous,
		"read_only":            !auth.WritingAllowed,
		"space":                map[string]interface{}{"recipe": bag},
		"tiddlywiki_version":   "5.2.3",  // TODO: How to identify this?
		"tiddlybucket_version": "v0.0.0", // TODO: How to identify this?
	})
}

func (h *handlerWithStore) getSkinnyTiddlerList(w http.ResponseWriter, r *http.Request) {
	recipe := chi.URLParam(r, "recipe")   // ignoring
	filter := r.URL.Query().Get("filter") // ignoring
	log.Debug().Str("recipe", recipe).Str("filter", filter).Msg("getSkinnyTiddlerList()")

	skinny := h.getSkinnyListCache()
	if len(skinny) <= 0 {
		log.Trace().Msg("creating skinny tiddler list")
		tids, err := h.Store.GetAllTiddlers()
		if err != nil {
			log.Error().Err(err).Msg("could not read tiddlers from store")
			http.Error(w, fmt.Sprintf("could not read tiddlers from store: %s", err.Error()),
				http.StatusInternalServerError)
			return
		}

		skinny = make([]Tiddler, 0)
		for i := range tids {
			if strings.HasPrefix(tids[i]["title"].(string), "$:/") {
				continue
			}
			var keeptext bool
			if tagsRaw, ok := tids[i]["tags"]; ok {
				// skinny list only unless the tiddler text if it's a macro
				// basing this entirely on @rsc's comment here: https://github.com/rsc/tiddly/blob/master/tiddly.go#L160-L164
				switch tagsRaw.(type) {
				case []interface{}:
					for _, t := range tagsRaw.([]interface{}) {
						if !strings.Contains(t.(string), "$:/tags/Macro") {
							keeptext = true
							break
						}
					}
					/*
							case []string:
						if !strings.Contains(strings.Join(tagsRaw.([]string), " "), "$:/tags/Macro") {
							keeptext = true
							break
						}
					*/
				case string, interface{}:
					if strings.Contains(tagsRaw.(string), "$:/tags/Macro") {
						keeptext = true
					}
				default:
					log.Fatal().Str("title", tids[i]["tags"].(string)).Str("tags_type", fmt.Sprintf("%T", tagsRaw)).Msg("unexpected type for tags field")
				}
			}
			// copy each field independently to make sure you don't mess with the store cache
			skinnyTid := make(Tiddler)
			for k, v := range tids[i] {
				if k == "text" && !keeptext {
					continue
				}
				skinnyTid[k] = v
			}
			skinny = append(skinny, skinnyTid)
			log.Trace().Str("title", skinnyTid["title"].(string)).Interface("skinny", skinnyTid).Msg("added to skinny list")
		}

		h.setSkinnyListCache(skinny)
	}

	render.JSON(w, r, skinny)
}

func (h *handlerWithStore) getTiddler(w http.ResponseWriter, r *http.Request) {
	recipe := chi.URLParam(r, "recipe") // unused
	tiddlerNameRaw := chi.URLParam(r, "*")
	if tiddlerNameRaw == "" {
		log.Error().Msg("tiddler name not provided")
		http.Error(w, "tiddler name not provided", http.StatusBadRequest)
		return
	}
	tiddlerName, err := url.PathUnescape(tiddlerNameRaw)
	if err != nil {
		log.Error().Str("tiddlerNameRaw", tiddlerNameRaw).Err(err).Msg("could not unescape tiddler name")
		http.Error(w, fmt.Sprintf("could not read tiddler name: %s", err.Error()), http.StatusInternalServerError)
		return
	}
	log.Debug().Str("recipe", recipe).Str("tiddlerName", tiddlerName).Msg("getTiddler")

	if tiddlerName == "$:/StoryList" {
		log.Info().Msg("skipped reading StoryList")
		return
	}

	tid, err := h.Store.GetTiddler(tiddlerName)
	if err != nil {
		log.Error().Err(err).Msg("could not read tiddler from store")
		http.Error(w, fmt.Sprintf("could not read tiddler from store: %s", err.Error()), http.StatusNotFound)
		return
	}

	log.Trace().Interface("tid", tid).Msg("found tiddler")

	render.JSON(w, r, tid)
}

func (h *handlerWithStore) putTiddler(w http.ResponseWriter, r *http.Request) {
	h.resetCaches()

	recipe := chi.URLParam(r, "recipe")
	tiddlerNameRaw := chi.URLParam(r, "*")
	if tiddlerNameRaw == "" {
		log.Error().Msg("tiddler name not provided")
		http.Error(w, "tiddler name not provided", http.StatusBadRequest)
		return
	}
	tiddlerName, err := url.PathUnescape(tiddlerNameRaw)
	if err != nil {
		log.Error().Str("tiddlerNameRaw", tiddlerNameRaw).Err(err).Msg("could not unescape tiddler name")
		http.Error(w, fmt.Sprintf("could not read tiddler name: %s", err.Error()), http.StatusInternalServerError)
		return
	}
	log.Debug().Str("recipe", recipe).Str("tiddlerName", tiddlerName).Msg("putTiddler")

	var newTiddler Tiddler
	if err := newTiddler.Read(r.Body); err != nil {
		log.Error().Err(err).Msg("could not read tiddler from request")
		http.Error(w, fmt.Sprintf("could not read tiddler from request: %s", err.Error()), http.StatusInternalServerError)
		return
	}
	// For some reason, the payload sent here is the only time this is an array and not a string
	if foundTags, ok := newTiddler["tags"]; ok {
		switch foundTags.(type) {
		case []string, []interface{}:
			tags := make([]string, 0)
			for _, t := range foundTags.([]interface{}) {
				t := t.(string)
				if len(strings.Split(t, " ")) > 1 {
					t = fmt.Sprintf("[[%s]]", t)
				}
				tags = append(tags, t)
			}
			newTiddler["tags"] = strings.Join(tags, " ")
		default:
		}
	}
	// same for the fields
	if foundFields, ok := newTiddler["fields"]; ok {
		for f, v := range foundFields.(map[string]interface{}) {
			newTiddler[f] = v
		}
		delete(newTiddler, "fields")
	}
	log.Trace().Interface("newTiddler", newTiddler).Send()

	revision := 0
	etag := func() string {
		return fmt.Sprintf("\"%s/%s/%d:%x\"", bag, url.QueryEscape(tiddlerName), revision, md5.Sum(newTiddler.Bytes()))
	}

	// TODO: check out that the etag passed in matches
	// TODO: the title of an existing tiddler is not overwritten

	if tiddlerName == "$:/StoryList" {
		log.Info().Msg("skipped writing StoryList")
		w.Header().Add("Etag", etag())
		render.NoContent(w, r)
		return
	}

	// get the rev of the existing, if it does exist
	if tid, err := h.Store.GetTiddler(tiddlerName); err == nil {
		old, _ := strconv.Atoi(tid.Field("revision"))
		revision += old
		newTiddler.setField("revision", strconv.Itoa(revision))
	}

	if err := h.Store.WriteTiddler(newTiddler); err != nil {
		log.Error().Err(err).Msg("could not add tiddler to store")
		http.Error(w, fmt.Sprintf("could not add tiddler to store: %s", err.Error()), http.StatusInternalServerError)
		return
	}

	if log.Logger.GetLevel() <= zerolog.TraceLevel {
		log.Trace().Interface("newTiddler", newTiddler).Msg("ostensibly wrote this?")
		if tid, err := h.Store.GetTiddler(tiddlerName); err == nil {
			log.Trace().Interface("tid", tid).Msg("reading it back")
		}
		log.Trace().Interface("newTiddler", newTiddler).Msg("ostensibly wrote this?")
		log.Trace().Str("title", newTiddler["title"].(string)).Msg("reseting index cache after PUT")
	}

	w.Header().Add("Etag", etag())
	render.NoContent(w, r)
}

func (h *handlerWithStore) deleteTiddler(w http.ResponseWriter, r *http.Request) {
	h.resetCaches()

	bag := chi.URLParam(r, "bag")
	tiddlerNameRaw := chi.URLParam(r, "*")
	if tiddlerNameRaw == "" {
		log.Error().Msg("tiddler name not provided")
		http.Error(w, "tiddler name not provided", http.StatusBadRequest)
		return
	}
	tiddlerName, err := url.PathUnescape(tiddlerNameRaw)
	if err != nil {
		log.Error().Str("tiddlerNameRaw", tiddlerNameRaw).Err(err).Msg("could not unescape tiddler name")
		http.Error(w, fmt.Sprintf("could not unescape tiddler name: %s", err.Error()), http.StatusInternalServerError)
	}
	log.Debug().Str("bag", bag).Str("tiddlerName", tiddlerName).Msg("deleteTiddler")

	if err := h.Store.DeleteTiddler(tiddlerName); err != nil {
		log.Error().Str("tiddlerName", tiddlerName).Err(err).Msg("could not delete tiddler from store")
		http.Error(w, fmt.Errorf("could not delete tiddler from store: %s", err.Error()).Error(), http.StatusInternalServerError)
	}
}

func zerologger(logger zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(rw http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(rw, r.ProtoMajor)
			start := time.Now()
			defer func() {
				q, _ := url.QueryUnescape(r.URL.RawQuery)
				var username string
				if auth, ok := r.Context().Value("auth").(authContext); ok {
					username = auth.Username
				}
				logger.Info().
					Str("protocol", r.Proto).
					Int("status", ww.Status()).
					Int("bytes", ww.BytesWritten()).
					Str("method", r.Method).
					Str("path", r.URL.Path).
					Str("query", q).
					Str("ip", r.RemoteAddr).
					Str("user-agent", r.UserAgent()).
					Str("username", username).
					Dur("latency", time.Since(start)).
					Send()
			}()

			next.ServeHTTP(ww, r)
		}
		return http.HandlerFunc(fn)
	}
}

type authContext struct {
	Username                       string
	CanBeAnonymous, WritingAllowed bool
}

func basicAuthCtx(w http.ResponseWriter, r *http.Request, creds Credentials) (authContext, bool) {
	var auth authContext
	auth.Username = AuthAnonUsername
	if creds.Writers == nil {
		auth.WritingAllowed = true
		auth.CanBeAnonymous = true
	} else if creds.Readers == nil {
		auth.CanBeAnonymous = true
	}
	log.Trace().
		Int("num_creds", len(creds.UserPasswordsClearText)).
		Strs("readers", creds.Readers).
		Strs("writers", creds.Writers).
		Msg("basicAuthCtx")
	var isAuthenticated bool
	if user, pass, ok := r.BasicAuth(); ok {
		log.Trace().Str("user", user).Bool("ok", ok).Msg("basicAuthCtx")
		credPass, credUserOk := creds.UserPasswordsClearText[user]
		log.Trace().Str("credPass", credPass).
			Bool("credUserOk", credUserOk).Msg("basicAuthCtx")
		if credUserOk && subtle.ConstantTimeCompare([]byte(pass), []byte(credPass)) == 1 {
			isAuthenticated = true
			auth.Username = user
			auth.CanBeAnonymous = false
			auth.WritingAllowed = creds.userCanWrite(user, isAuthenticated)
			log.Trace().Bool("isAuthenticated", isAuthenticated).
				Interface("auth", auth).Msg("basicAuthCtx")
		}
	}

	// Skipping login-basic seems like a hack...
	if r.URL.Path != "/login-basic" && !isAuthenticated && creds.Readers != nil {
		return authContext{}, false
	}

	return auth, true
}

func getIPAddress() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	// handle err
	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			return "", err
		}
		// handle err
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			// process IP address
			if !ip.IsLoopback() {
				if ip.To4() != nil {
					return ip.String(), nil
				}
			}
		}
	}
	return "", err
}

//Pass handlerMap to ListenAndServe from main.go.
//handlerMap has to implement handlerWithStore interface and proxy calls to the correct handlerWithStore instance for each wiki
func ListenAndServe(addr string, credentialsFile string, readers string, writers string, storageType string, storageLocation string) error {

	var store TiddlerStore
	var err error

	serverHostAndPort = addr
	serverBasePath = filepath.Dir(storageLocation)
	serverTrashLocation = filepath.Join(serverBasePath, "trash") //Trash location for deleted wikis. Purge after some number of days.
	serverStorageType = storageType
	serverStorageLocation = storageLocation
	routeHandlers = &handlerRouter{handlerWithStoreMap: map[string]*handlerWithStore{}}

	switch storageType {
	case "file":
		//Get list of directories in the wiki location. Each subdirectory hosts a separate wiki.
		wikiFolders, err := getWikiFolders(storageLocation)
		if err != nil {
			return err
		}
		//Temporary hack:  If no wiki folders under storageLocation, assume a default folder called "wiki"
		//POSSIBLE FIX: default to storageLocation as it is today (one wiki), or add directory listing functions for Google and AWS
		if len(wikiFolders) == 0 {
			wikiFolders = append(wikiFolders, "wiki")
		}
		for _, wiki := range wikiFolders {
			//Create a handlerWithStore for each wiki and add to routeHandlers.
			wikiPath := filepath.Join(storageLocation, wiki)
			fmt.Println("Wiki Path to STORE: ", storageLocation, "+", wiki, "=", wikiPath)
			store, err = NewFileStore(wikiPath)
			handler := &handlerWithStore{Store: store}
			handler.setCustomPath(wiki)
			fmt.Println("Adding handler: ", wiki, " = ", fmt.Sprintf("%T", handler))
			routeHandlers.addHandler(wiki, handler)
		}
	case "gs":
		//TODO Get list of directories in the wiki location. Each subdirectory hosts a separate wiki.
		//wikiFolders, err := getWikiFolders(storageLocation)
		//if err != nil {
		//	return err
		//}
		//for _, wiki := range wikiFolders {
		//Create a handlerWithStore for each wiki and add to routeHandlers.
		//store, err = NewGoogleBucketStore(filepath.Join(storageLocation, wiki))
		store, err = NewGoogleBucketStore(storageLocation)
		handler := &handlerWithStore{Store: store}
		routeHandlers.addHandler("wiki", handler)
		//}
	case "s3":
		//TODO Get list of directories in the wiki location. Each subdirectory hosts a separate wiki.
		//wikiFolders, err := getWikiFolders(storageLocation)
		//if err != nil {
		//	return err
		//}
		//for _, wiki := range wikiFolders {
		//Create a handlerWithStore for each wiki and add to routeHandlers.
		//store, err = NewAwsS3Store(filepath.Join(storageLocation, wiki))
		store, err = NewAwsS3Store(storageLocation)
		handler := &handlerWithStore{Store: store}
		routeHandlers.addHandler("wiki", handler)
		//}
	default:
		err = fmt.Errorf("error: storage type not supported")
	}
	if err != nil {
		log.Panic().Str("storage_type", storageType).Err(err).Msg("could not create TiddlerStore")
	}

	// Identify credentials, if applicable
	insecureCreds, err := creds(store, credentialsFile, readers, writers)
	if err != nil {
		log.Panic().Str("credentials file", credentialsFile).Err(err).Msg("unable to process credentials")
	}

	r := chi.NewRouter()
	r.Use(zerologger(log.Logger))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth, ok := basicAuthCtx(w, r, insecureCreds)
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), "auth", auth)))
		})
	})
	r.Use(middleware.Compress(5, "text/html", "text/css", "text/javascript"))
	r.Use(middleware.Recoverer)
	r.Use(middleware.SetHeader("Connection", "keep-alive"))
	r.Use(middleware.SetHeader("Keep-Alive", "timeout=5"))

	r.Get("/", serverRootIndex)                            //Load the root index.html page that lists the wikis served by this server and instructs on how to create new ones.
	r.Get("/addWiki", addWiki)                             //Display a page to enable user to create a new wiki from a template.
	r.Get("/createNewWiki", createNewWiki)                 //Create the new wiki with name (required) and template (default server edition if omitted).
	r.Get("/deleteWiki", deleteWiki)                       //Delete a wiki. Confirm deletion. Copy to purgatory for some period of time to allow for recovery.
	r.Get("/{wiki}/login-basic", routeHandlers.loginBasic) //Keep this the same for now. Assume single user. After multiple wikis, consider support for multiple users.
	r.Get("/{wiki}", routeHandlers.index)                  //Use a named parameter to serve the index for the designated wiki. e.g. "/{wikifolder}". Enable create wiki if does not exist.
	r.Get("/{wiki}/favicon.ico", routeHandlers.favicon)    //Use a named parameter. e.g. "/{wikifolder}/favicon.ico"

	r.Group(func(r chi.Router) {
		r.Use(render.SetContentType(render.ContentTypeJSON))

		r.Get("/{wiki}/status", routeHandlers.status) //Use a named parameter.

		r.Get("/{wiki}/recipes/{recipe}/tiddlers.json", routeHandlers.getSkinnyTiddlerList) //Use a named parameter. e.g. "/{wikifolder}/recipes/{recipe}/tiddlers.json"
		r.Get("/{wiki}/recipes/{recipe}/tiddlers/*", routeHandlers.getTiddler)              //Use a named parameter.
		r.Put("/{wiki}/recipes/{recipe}/tiddlers/*", routeHandlers.putTiddler)              //Use a named parameter.
		r.Delete("/{wiki}/bags/{bag}/tiddlers/*", routeHandlers.deleteTiddler)              //Use a named parameter.
	})

	log.Info().Str("addr", addr).Msg("starting server")
	return http.ListenAndServe(serverHostAndPort, r)
}

func creds(store TiddlerStore, credentialsFile, readers, writers string) (Credentials, error) {
	var insecureCreds Credentials
	insecureCreds.UserPasswordsClearText = make(map[string]string)

	if credentialsFile != "" {
		fileReader, err := store.ReadFile(credentialsFile)
		if err != nil {
			return insecureCreds, fmt.Errorf("could not find the credentials file")
		}
		reader := csv.NewReader(fileReader)

		records, err := reader.ReadAll()
		if err != nil {
			return insecureCreds, fmt.Errorf("could not read credentials file")
		}
		for _, r := range records[1:] {
			insecureCreds.UserPasswordsClearText[r[0]] = r[1]
			log.Trace().Interface("record", r).Msg("credentials")
		}
	}
	// Readers
	if strings.Contains(readers, ",") {
		insecureCreds.Readers = strings.Split(readers, ",")
	} else if readers == authTokenAuthenticated {
		insecureCreds.Readers = make([]string, 0)
		for k := range insecureCreds.UserPasswordsClearText {
			insecureCreds.Readers = append(insecureCreds.Readers, k)
		}
	}
	// Writers
	if strings.Contains(writers, ",") {
		insecureCreds.Writers = strings.Split(writers, ",")
	} else if writers == authTokenAuthenticated {
		insecureCreds.Writers = make([]string, 0)
		for k := range insecureCreds.UserPasswordsClearText {
			insecureCreds.Writers = append(insecureCreds.Writers, k)
		}
	}

	return insecureCreds, nil
}
