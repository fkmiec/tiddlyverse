package tiddlybucket

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/rs/zerolog/log"
	"google.golang.org/api/iterator"
)

const numWorkers = 15

var (
	reTiddlerFilename = regexp.MustCompile(`[/:"]`)
	reBinaryType      = regexp.MustCompile(`/(pdf|gif|jpeg|png|x-icon)$`)
)

type TiddlerStore interface {
	ReadFile(path string) (io.ReadCloser, error)
	GetTiddler(title string) (Tiddler, error)
	GetAllTiddlers() ([]Tiddler, error)
	WriteTiddler(t Tiddler) error
	DeleteTiddler(title string) error
	//Added below functions to support creation and management of multiple wikis
	CreateRequiredFolders(path string) error
	GetWikiList(path string) ([]string, error)
	GetWikiTemplateList(path string) ([][]string, error)
	CreateWikiFolder(wikiPath string, templateFilePath string) error
	CopyFolder(srcPath string, targetPath string) error
	DeleteFolder(path string) error
}

func tiddlerFilename(title string) string {
	return fmt.Sprintf("%s.tid", reTiddlerFilename.ReplaceAllString(title, "_"))
}

func isTiddlerFile(path string) bool {
	if strings.HasPrefix(path, ".") || (!strings.HasSuffix(path, ".tid") && !strings.HasSuffix(path, ".meta")) {
		return false
	}

	if strings.HasSuffix(path, "$__plugins_tobibeer_rate_styles_imgfix.tid") {
		// FIXME: fuck you in particular
		return false
	}
	return true
}

func writeTiddlerToWriter(t Tiddler, tiddlersDir string, index *map[string]string, cache *map[string]Tiddler, writer func(path string) (io.WriteCloser, error)) error {
	title := t.Field("title") // TODO: remove?
	path := filepath.Join(tiddlersDir, tiddlerFilename(title))
	log.Trace().Str("title", title).Str("path", path).Msg("writeTiddlerToWriter")

	w, err := writer(path)
	if err != nil {
		return err
	}
	defer w.Close()

	tfile := TiddlerFile{t}
	if err := tfile.Write(w); err == nil {
		(*index)[title] = path
		(*cache)[title] = t
	}
	// log.Trace().Interface("tfile", tfile).Msg("writeTiddlerToWriter")

	return err
}

func readTiddlerFileWithReadCloser(path string, reader func(path string) (io.ReadCloser, error)) (Tiddler, error) {
	fmt.Printf("DEBUG: readTiddlerFileWithReadCloser(): %s", path)
	log.Trace().Str("path", path).Msg("readTiddlerFileWithReadCloser()")
	f, err := reader(path)
	if err != nil {
		return nil, fmt.Errorf("could not open file '%s': %s", path, err.Error())
	}
	defer f.Close()

	var tfile TiddlerFile
	if err := tfile.Read(f); err != nil {
		return nil, fmt.Errorf("could not read file '%s' as tiddler: %s", path, err.Error())
	}
	log.Trace().Interface("tfile", tfile).Msg("read file from tiddler")

	// if filename is meta, then read that file in as text
	if strings.HasSuffix(path, ".meta") {
		nonMetaFilename := strings.TrimSuffix(path, ".meta")

		//read full contents of file
		mf, err := reader(nonMetaFilename)
		if err != nil {
			log.Error().Err(err).
				Str("non_meta_filename", nonMetaFilename).
				Str("path", path).
				Msg("could not open file")
			return nil, fmt.Errorf("could not open file '%s': %s", nonMetaFilename, err.Error())
		}
		defer mf.Close()

		b, err := io.ReadAll(mf)
		if err != nil {
			log.Error().Err(err).
				Str("non_meta_filename", nonMetaFilename).
				Str("path", path).
				Msg("could not read file contents")
			return nil, fmt.Errorf("could not read file contents '%s': %s", nonMetaFilename, err.Error())
		}

		log.Trace().Str("type", tfile.tid["type"].(string)).Msg("check type")
		if reBinaryType.MatchString(tfile.tid["type"].(string)) {
			tfile.tid["text"] = b
		} else {
			tfile.tid["text"] = string(b)
		}
	}

	return tfile.Tiddler(), nil
}

func getTiddlerFileFromStore(title, tiddlersDir string, index map[string]string, cache map[string]Tiddler, reader func(string) (io.ReadCloser, error)) (Tiddler, error) {
	log.Debug().Str("title", title).Msg("get file from store")
	filename, ok := index[title]
	if !ok {
		filename = filepath.Join(tiddlersDir, tiddlerFilename(title))
		log.Warn().Str("title", title).Str("filename", filename).
			Msg("generated filename for unindexed title")
	}
	if tiddler, ok := cache[filename]; ok {
		log.Trace().Str("title", title).Msg("loading from cache")
		return tiddler, nil
	}
	// TODO: load unindexed file?
	return readTiddlerFileWithReadCloser(filename, reader)
}

func getAllTiddlerFilesFromStore(cache map[string]Tiddler, reader func(string) (io.ReadCloser, error), walker func(func(string) error) error) ([]Tiddler, error) {
	tids := make([]Tiddler, 0)
	if len(cache) > 0 {
		tids := make([]Tiddler, 0)
		for _, t := range cache {
			tids = append(tids, t)
		}
		return tids, nil
	}

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	paths := make(chan string, numWorkers)
	for w := 1; w <= numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range paths {
				tid, err := readTiddlerFileWithReadCloser(path, reader)
				if err != nil {
					log.Error().Err(err).Msg("could not get tiddler as file")
					// TODO: return err?
				}
				if _, ok := tid["title"]; !ok {
					panic(fmt.Sprintf("shit: %s", path))
				}

				mu.Lock()
				tids = append(tids, tid)
				mu.Unlock()
			}
		}()
	}

	err := walker(func(path string) error {
		paths <- path
		return nil
	})
	close(paths)
	if err != nil {
		return nil, err
	}

	wg.Wait()

	return tids, nil
}

func buildCacheAndIndex(walker func(f func(path string) error) error,
	reader func(path string) (io.ReadCloser, error)) (map[string]string, map[string]Tiddler, error) {
	start := time.Now()

	index := make(map[string]string)
	cache := make(map[string]Tiddler)

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	paths := make(chan string, numWorkers)
	for w := 1; w <= numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range paths {
				tiddler, err := readTiddlerFileWithReadCloser(path, reader)
				if err != nil {
					log.Error().Err(err).Msg("could not get tiddler as file")
					// TODO: return err
				}
				if title, ok := tiddler["title"]; ok {
					title := title.(string)

					mu.Lock()
					index[title] = path
					cache[title] = tiddler
					mu.Unlock()
				}
				// TODO: deal with title-less tiddlers
			}
		}()
	}

	log.Trace().Msg("calling walker")
	err := walker(func(path string) error {
		log.Trace().Str("path", path).Msg("sending to index")
		paths <- path

		return nil
	})
	log.Trace().Msg("closing paths channel")
	close(paths)
	if err != nil {
		log.Trace().Err(err).Msg("error walking paths")
		// TODO: better
		return nil, nil, err
	}

	wg.Wait()
	log.Info().
		Int("num_tiddlers", len(index)).
		Dur("ellapsed", time.Since(start)).
		Float64("ellapsed_min", time.Since(start).Minutes()).
		Msg("tiddler index and cache built")

	return index, cache, nil
}

type fileStore struct {
	baseDir, tiddlersDir string
	tiddlerToFile        map[string]string
	tiddlerCache         map[string]Tiddler
}

func (s *fileStore) newReader(filename string) (io.ReadCloser, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("could not open file '%s': %s", filename, err.Error())
	}
	return f, nil
}

func (s *fileStore) walk(f func(path string) error) error {
	errors := make([]error, 0)
	fileSystem := os.DirFS(s.tiddlersDir)
	err := fs.WalkDir(fileSystem, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// FIXME: need to handle meta files (I guess the whole content of the non-meta goes in the text field?)
		if d.IsDir() || !isTiddlerFile(path) {
			return nil
		}

		if err := f(filepath.Join(s.tiddlersDir, path)); err != nil {
			errors = append(errors, err)
			return nil
		}

		return nil
	})
	if err != nil {
		panic(err)
	}

	for _, err := range errors {
		log.Error().Err(err).Msg("GetAll error")
	}

	return nil // TODO: fix this error handling
}

func (s *fileStore) ReadFile(path string) (io.ReadCloser, error) {
	return s.newReader(filepath.Join(s.baseDir, path))
}

func (s *fileStore) GetTiddler(title string) (Tiddler, error) {
	return getTiddlerFileFromStore(title, s.tiddlersDir, s.tiddlerToFile, s.tiddlerCache, s.newReader)
}

func (s *fileStore) GetAllTiddlers() ([]Tiddler, error) {
	return getAllTiddlerFilesFromStore(s.tiddlerCache, s.newReader, s.walk)
}

func (s *fileStore) WriteTiddler(t Tiddler) error {
	return writeTiddlerToWriter(t, s.tiddlersDir, &(s.tiddlerToFile), &(s.tiddlerCache), func(path string) (io.WriteCloser, error) {
		w, err := os.Create(path)
		if err != nil {
			return nil, err
		}
		return w, nil
	})
}

func (s *fileStore) DeleteTiddler(title string) error {
	log.Trace().Str("title", title).Str("filename", s.tiddlerToFile[title]).
		Msg("fileStore.Delete")
	if err := os.Remove(s.tiddlerToFile[title]); err != nil {
		return err
	}
	delete(s.tiddlerToFile, title)
	delete(s.tiddlerCache, title)
	return nil
}

func (s *fileStore) CreateRequiredFolders(path string) error {
	//Create wikis, templates and trash folders in the indicated storage location if they do not exist
	var err error
	wikisDirExists := false
	templatesDirExists := false
	trashDirExists := false

	files, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.IsDir() {
			if file.Name() == "wikis" {
				wikisDirExists = true
			} else if file.Name() == "templates" {
				templatesDirExists = true
			} else if file.Name() == "trash" {
				trashDirExists = true
			}
		}
	}
	if !wikisDirExists {
		err = os.Mkdir(filepath.Join(path, "wikis"), 0700)
		if err != nil {
			return err
		}
	}
	if !templatesDirExists {
		err = os.Mkdir(filepath.Join(path, "templates"), 0700)
		if err != nil {
			return err
		}
	}
	if !trashDirExists {
		err = os.Mkdir(filepath.Join(path, "trash"), 0700)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *fileStore) GetWikiList(path string) ([]string, error) {
	wikiFolders := []string{}

	files, err := os.ReadDir(path)
	if err != nil {
		panic(err)
	}

	for _, file := range files {
		if file.IsDir() {
			wikiFolders = append(wikiFolders, file.Name())
		}
	}

	return wikiFolders, nil
}

//Return a map of template names to slice of file path and description.
func (s *fileStore) GetWikiTemplateList(path string) ([][]string, error) {
	templates := map[string][]string{}
	files, err := os.ReadDir(path)
	if err != nil {
		panic(err)
	}

	for _, file := range files {
		var filename string
		var templateName string
		var parts []string
		var values []string

		if !file.IsDir() {
			filename = file.Name()
			parts = strings.Split(file.Name(), ".")
			templateName = parts[0]
			values = templates[templateName]
			if values == nil {
				values = make([]string, 2)
				templates[templateName] = values
			}
			if parts[1] == "txt" {
				//read in the description
				f, err := os.Open(filepath.Join(path, filename))
				if err != nil {
					return nil, fmt.Errorf("could not open file '%s': %s", filename, err.Error())
				}
				reader := bufio.NewReader(f)
				var descriptionBytes bytes.Buffer
				for {
					line, err := reader.ReadString('\n')
					if err != nil && err != io.EOF {
						log.Fatal().Str("line", line).Err(err).Msg("could not read line in template description file")
					}
					descriptionBytes.WriteString(line)
					if err == io.EOF {
						break
					}
				}
				values[1] = descriptionBytes.String()
			} else {
				values[0] = filename
			}
		}
	}
	templateValues := make([][]string, len(templates))
	i := 0
	for k := range templates {
		templateValues[i] = make([]string, 3)
		templateValues[i][0] = k               //template name
		templateValues[i][1] = templates[k][0] //template file
		templateValues[i][2] = templates[k][1] //template description
		i++
	}
	//Sort by template name
	sort.SliceStable(templateValues, func(i, j int) bool {
		return templateValues[i][0] < templateValues[j][0]
	})

	return templateValues, nil
}

func (s *fileStore) CreateWikiFolder(wikiPath string, templateFilePath string) error {
	//Create a new directory under wiki folder with the provided name
	err := os.Mkdir(wikiPath, 0700)
	if err != nil {
		return err
	}
	//Copy the designated template file to the new directory and rename as index.html
	input, err := os.ReadFile(templateFilePath)
	if err != nil {
		return err
	}
	destinationFile := filepath.Join(wikiPath, "index.html")
	err = os.WriteFile(destinationFile, input, 0644)
	if err != nil {
		return err
	}
	//Create a tiddlers folder in the new directory
	err = os.Mkdir(filepath.Join(wikiPath, "tiddlers"), 0700)
	if err != nil {
		return err
	}
	return nil
}

func (s *fileStore) CopyFolder(srcPath string, targetPath string) error {
	//Create a new directory under targetPath folder with the provided name
	err := CopyDir(srcPath, targetPath)
	if err != nil {
		return err
	}
	return nil
}

func (s *fileStore) DeleteFolder(path string) error {
	err := os.RemoveAll(path)
	if err != nil {
		return err
	}
	return nil
}

func NewFileStore(dir string, requireIndex bool) (TiddlerStore, error) {
	log.Info().Str("dir", dir).Msg("creating 'local filesystem' TiddlerStore")

	s := new(fileStore)
	s.baseDir = dir
	s.tiddlersDir = filepath.Join(s.baseDir, "tiddlers")

	if requireIndex { //Index for tiddlers and wiki index.html file not needed for handler that will solely manage wikis, templates and trash folders
		// build the index
		// if err := s.rebuildIndex(); err != nil {
		index, cache, err := buildCacheAndIndex(s.walk, s.newReader)
		if err != nil {
			return nil, err
		}
		s.tiddlerToFile = index
		s.tiddlerCache = cache
	}

	return s, nil
}

// https://pkg.go.dev/google.golang.org/cloud/storage#hdr-Creating_a_Client
type googleBucketStore struct {
	uri, bucket, baseDir, tiddlersDir string
	tiddlerToFile                     map[string]string
	tiddlerCache                      map[string]Tiddler
	client                            *storage.Client
	ctx                               context.Context
	bucketHandle                      *storage.BucketHandle
}

func (s *googleBucketStore) newReader(path string) (io.ReadCloser, error) {
	r, err := s.bucketHandle.Object(path).NewReader(s.ctx)
	if err != nil {
		log.Warn().Str("path", path).Err(err).Msg("could not create tiddler reader")
		return nil, fmt.Errorf("could not open object '%s': %s", path, err.Error())
	}
	return r, nil
}

func (s *googleBucketStore) walk(f func(filename string) error) error {
	errors := make([]error, 0)
	it := s.bucketHandle.Objects(s.ctx, &storage.Query{Prefix: s.tiddlersDir})
	p := iterator.NewPager(it, 100, "")
	var wg sync.WaitGroup
	for {
		var objects []*storage.ObjectAttrs
		log.Trace().Msg("calling p.NextPage()")
		pageToken, err := p.NextPage(&objects)
		if err != nil {
			log.Panic().Err(err).Msg("error reading gcp bucket page")
			// TODO: Handle error.
		}
		log.Trace().Int("len(objects)", len(objects)).Str("pageToken", pageToken).Msg("have objects")
		wg.Add(1)
		go func(entries []*storage.ObjectAttrs) {
			defer wg.Done()
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name, s.tiddlersDir) && isTiddlerFile(entry.Name) {
					log.Trace().Str("name", entry.Name).Msg("found a valid tiddler")
					if err := f(entry.Name); err != nil {
						errors = append(errors, err)
					}
				}
			}
		}(objects)
		if pageToken == "" {
			wg.Wait()
			break
		}
	}

	for _, err := range errors {
		log.Error().Err(err).Msg("walk error")
	}

	return nil // TODO: fix this error handling
}

func (s *googleBucketStore) ReadFile(path string) (io.ReadCloser, error) {
	return s.newReader(filepath.Join(s.baseDir, path))
}

func (s *googleBucketStore) GetTiddler(title string) (Tiddler, error) {
	return getTiddlerFileFromStore(title, s.tiddlersDir, s.tiddlerToFile, s.tiddlerCache, s.newReader)
}

func (s *googleBucketStore) GetAllTiddlers() ([]Tiddler, error) {
	return getAllTiddlerFilesFromStore(s.tiddlerCache, s.newReader, s.walk)
}

func (s *googleBucketStore) WriteTiddler(t Tiddler) error {
	log.Trace().Str("title", t["title"].(string)).Msg("googleBucketStore.WriteTiddler")
	return writeTiddlerToWriter(t, s.tiddlersDir, &(s.tiddlerToFile), &(s.tiddlerCache), func(path string) (io.WriteCloser, error) {
		return s.bucketHandle.Object(path).NewWriter(s.ctx), nil
	})
}

func (s *googleBucketStore) DeleteTiddler(title string) error {
	log.Trace().Str("title", title).Str("filename", s.tiddlerToFile[title]).
		Msg("googleBucketStore.Delete")
	if err := s.bucketHandle.Object(s.tiddlerToFile[title]).Delete(s.ctx); err != nil {
		return err
	}
	delete(s.tiddlerToFile, title)
	delete(s.tiddlerCache, title)
	return nil
}

//Creates wikis, templates and trash folders at the specified wiki_location if not existing.
func (s *googleBucketStore) CreateRequiredFolders(path string) error {
	return errors.New("not yet implemented")
}

//Returns the list of existing wikis
func (s *googleBucketStore) GetWikiList(path string) ([]string, error) {
	return nil, errors.New("not yet implemented")
}

//Returns the list of existing wiki templates
func (s *googleBucketStore) GetWikiTemplateList(path string) ([][]string, error) {
	return nil, errors.New("not yet implemented")
}

//Creates the wiki folder, tiddlers folder and copies the relevant template wiki file to the wiki folder
func (s *googleBucketStore) CreateWikiFolder(wikiPath string, templateFilePath string) error {
	return errors.New("not yet implemented")
}

//Recursively copies a folder. Used to move a wiki and its tiddlers to the trash folder
func (s *googleBucketStore) CopyFolder(srcPath string, targetPath string) error {
	return errors.New("not yet implemented")
}

//Recursively deletes a folder and its contents. Used to remove a wiki folder from wikis after copying to trash.
func (s *googleBucketStore) DeleteFolder(path string) error {
	return errors.New("not yet implemented")
}

func NewGoogleBucketStore(uri string, requireIndex bool) (TiddlerStore, error) {
	log.Info().Str("uri", uri).Msg("creating 'Google Cloud Storage' TiddlerStore")

	var err error

	s := new(googleBucketStore)
	s.uri = uri
	u, err := url.Parse(s.uri)
	if err != nil {
		return nil, fmt.Errorf("could not parse the uri: %s", err)
	}
	s.bucket = u.Host
	s.baseDir = u.Path[1:]
	s.tiddlersDir = filepath.Join(s.baseDir, "tiddlers")
	log.Trace().Str("bucket", s.bucket).Str("tiddlersDir", s.tiddlersDir).Msg("parsed the uri")

	s.ctx = context.Background()
	s.client, err = storage.NewClient(s.ctx)
	if err != nil {
		log.Panic().Err(err).Msg("error creating gcp storage client")
		// TODO: Handle error.
	}
	s.bucketHandle = s.client.Bucket(s.bucket)

	if requireIndex { //Index not required for Store that will solely manage wikis, templates and trash folders
		// build the index
		index, cache, err := buildCacheAndIndex(s.walk, s.newReader)
		if err != nil {
			return nil, err
		}
		s.tiddlerToFile = index
		s.tiddlerCache = cache
	}
	return s, nil
}

// https://docs.aws.amazon.com/sdk-for-go/api/service/s3/
type awsS3Store struct {
	uri, bucket, baseDir, tiddlersDir string
	tiddlerToFile                     map[string]string
	tiddlerCache                      map[string]Tiddler
	s3svc                             *s3.S3
}

func (s *awsS3Store) newReader(path string) (io.ReadCloser, error) {
	result, err := s.s3svc.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path)})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case s3.ErrCodeNoSuchKey:
				log.Error().Str("path", path).Err(aerr).Msg(s3.ErrCodeNoSuchKey)
			case s3.ErrCodeInvalidObjectState:
				log.Error().Str("path", path).Err(aerr).Msg(s3.ErrCodeInvalidObjectState)
			default:
				log.Error().Str("path", path).Err(aerr).Msg("awserr: could not get reader for object")
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			log.Error().Str("path", path).Err(err).Msg("unknown error: could not get reader for object")
		}

		log.Warn().Str("path", path).Err(err).Msg("could not create tiddler reader")
		return nil, fmt.Errorf("could not open object '%s': %s", path, err.Error())
	}

	return result.Body, nil
}

func (s *awsS3Store) walk(f func(filename string) error) error {
	errors := make([]error, 0)
	result, err := s.s3svc.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket)})
	// Prefix: s.tiddlersDir})
	if err != nil {
		// errors = append(errors, err)
		log.Panic().Err(err).Msg("error reading s3 bucket objects")
		// TODO: Handle error.
		return err
	}

	for _, obj := range result.Contents {
		if strings.HasPrefix(*obj.Key, s.tiddlersDir) && isTiddlerFile(*obj.Key) {
			if err := f(*obj.Key); err != nil {
				// errors = append(errors, err)
				return nil
			}
		}
	}

	// Example iterating over at most 3 pages of a ListObjectsV2 operation.
	/*
		pageNum := 0
		err := client.ListObjectsV2Pages(params,
			func(page *s3.ListObjectsV2Output, lastPage bool) bool {
				pageNum++
				fmt.Println(page)
				return pageNum <= 3
			})
	*/

	for _, err := range errors {
		log.Error().Err(err).Msg("walk error")
		/*
			if aerr, ok := err.(awserr.Error); ok {
				switch aerr.Code() {
				case s3.ErrCodeNoSuchBucket:
					fmt.Println(s3.ErrCodeNoSuchBucket, aerr.Error())
				default:
					fmt.Println(aerr.Error())
				}
			} else {
				// Print the error, cast err to awserr.Error to get the Code and
				// Message from an error.
				fmt.Println(err.Error())
			}
		*/
	}

	return nil // TODO: fix this error handling
}

func (s *awsS3Store) ReadFile(path string) (io.ReadCloser, error) {
	return s.newReader(filepath.Join(s.baseDir, path))
}

func (s *awsS3Store) GetTiddler(title string) (Tiddler, error) {
	return getTiddlerFileFromStore(title, s.tiddlersDir, s.tiddlerToFile, s.tiddlerCache, s.newReader)
}

func (s *awsS3Store) GetAllTiddlers() ([]Tiddler, error) {
	return getAllTiddlerFilesFromStore(s.tiddlerCache, s.newReader, s.walk)
}

type s3ObjectWriteCloser struct {
	bucket, key string
	s3svc       *s3.S3
}

func (s s3ObjectWriteCloser) Write(p []byte) (int, error) {
	_, err := s.s3svc.PutObject(&s3.PutObjectInput{
		Body:   aws.ReadSeekCloser(bytes.NewReader(p)),
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.key),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			default:
				return 0, aerr
			}
		}
		return 0, err
	}
	return len(p), nil
}

func (s s3ObjectWriteCloser) Close() error {
	return nil
}

func (s *awsS3Store) WriteTiddler(t Tiddler) error {
	return writeTiddlerToWriter(t, s.tiddlersDir, &(s.tiddlerToFile), &(s.tiddlerCache), func(path string) (io.WriteCloser, error) {
		return s3ObjectWriteCloser{
			bucket: s.bucket,
			key:    path,
			s3svc:  s.s3svc,
		}, nil
	})
}

func (s *awsS3Store) DeleteTiddler(title string) error {
	_, err := s.s3svc.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.tiddlerToFile[title]),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			default:
				return aerr
			}
		}
		return err
	}
	delete(s.tiddlerToFile, title)
	delete(s.tiddlerCache, title)
	return nil
}

//Creates wikis, templates and trash folders at the specified wiki_location if not existing.
func (s *awsS3Store) CreateRequiredFolders(path string) error {
	return errors.New("Not yet implemented!")
}

//Returns the list of existing wikis
func (s *awsS3Store) GetWikiList(path string) ([]string, error) {
	return nil, errors.New("Not yet implemented!")
}

//Returns the list of existing wiki templates
func (s *awsS3Store) GetWikiTemplateList(path string) ([][]string, error) {
	return nil, errors.New("Not yet implemented!")
}

//Creates the wiki folder, tiddlers folder and copies the relevant template wiki file to the wiki folder
func (s *awsS3Store) CreateWikiFolder(wikiPath string, templateFilePath string) error {
	return errors.New("Not yet implemented!")
}

//Recursively copies a folder. Used to move a wiki and its tiddlers to the trash folder
func (s *awsS3Store) CopyFolder(srcPath string, targetPath string) error {
	return errors.New("Not yet implemented!")
}

//Recursively deletes a folder and its contents. Used to remove a wiki folder from wikis after copying to trash.
func (s *awsS3Store) DeleteFolder(path string) error {
	return errors.New("Not yet implemented!")
}

func NewAwsS3Store(uri string, requireIndex bool) (TiddlerStore, error) {
	log.Info().Str("uri", uri).Msg("creating 'AWS S3' TiddlerStore")

	var err error

	s := new(awsS3Store)
	s.uri = uri
	u, err := url.Parse(s.uri)
	if err != nil {
		return nil, fmt.Errorf("could not parse the uri: %s", err)
	}
	s.bucket = u.Host
	s.baseDir = u.Path[1:]
	s.tiddlersDir = filepath.Join(s.baseDir, "tiddlers")
	log.Trace().Str("bucket", s.bucket).Str("tiddlersDir", s.tiddlersDir).Msg("parsed the uri")

	sess := session.Must(session.NewSession())
	s.s3svc = s3.New(sess)

	if requireIndex { //Index not required for Store that will solely manage wikis, templates and trash folders
		// build the index
		index, cache, err := buildCacheAndIndex(s.walk, s.newReader)
		if err != nil {
			return nil, err
		}
		s.tiddlerToFile = index
		s.tiddlerCache = cache
	}
	return s, nil
}
