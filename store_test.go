package tiddlybucket

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type closingBuffer struct {
	bytes.Buffer
}

func (c *closingBuffer) Close() error { return nil }

func Test_writeTiddlerToWriter(t *testing.T) {
	dummyAsTid := getTestTiddlerJsonAsTid(t, "TestTiddler.json")
	type args struct {
		t           Tiddler
		tiddlersDir string
		index       map[string]string
		cache       map[string]Tiddler
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{"good test", args{dummyAsTid, "dummyTiddlerDir", make(map[string]string), make(map[string]Tiddler)}, false},
		// TODO: Add more test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title := tt.args.t["title"].(string)
			wantPath := filepath.Join(tt.args.tiddlersDir, tiddlerFilename(title))
			gotFile := new(closingBuffer)
			err := writeTiddlerToWriter(tt.args.t, tt.args.tiddlersDir, &tt.args.index, &tt.args.cache,
				func(path string) (io.WriteCloser, error) {
					if wantPath != path {
						t.Errorf("writeTiddlerToWriter() path does not match expected = %s, want %s", wantPath, path)
					}
					return gotFile, nil
				})
			if (err != nil) != tt.wantErr {
				t.Errorf("writeTiddlerToWriter() error = %v, wantErr %v", err, tt.wantErr)
			}
			var gotTid TiddlerFile
			if err := gotTid.Read(strings.NewReader(gotFile.String())); err != nil {
				t.Errorf("unable to use TiddlerFile.Read() to load the output of TiddlerFile.Write(): %v", err)
				return
			}
			if !tt.wantErr && !areTiddlersEqual(t, tt.args.t, gotTid.Tiddler()) {
				t.Errorf("writeTiddlerToWriter(): returned tiddler does not match expected")
			}
			if gotIndexed, ok := tt.args.index[title]; !tt.wantErr && (!ok || gotIndexed != wantPath) {
				t.Errorf("writeTiddlerToWriter(): index does not contain expected patht = %s, want = %s", gotIndexed, wantPath)
			}
			if gotCached, ok := tt.args.cache[title]; !tt.wantErr && (!ok || !areTiddlersEqual(t, tt.args.t, gotCached)) {
				t.Errorf("writeTiddlerToWriter(): cached tiddler does not match expected")
			}
		})
	}
}

func Test_readTiddlerFileWithReadCloser(t *testing.T) {
	dummyAsTid := getTestTiddlerJsonAsTid(t, "TestTiddler.json")
	osReader := func(filename string) (io.ReadCloser, error) {
		f, err := os.Open(filename)
		if err != nil {
			return nil, fmt.Errorf("could not open file '%s': %s", filename, err.Error())
		}
		return f, nil
	}
	type args struct {
		path   string
		reader func(path string) (io.ReadCloser, error)
	}
	tests := []struct {
		name    string
		args    args
		want    Tiddler
		wantErr bool
	}{
		{"standard tiddler", args{filepath.Join(testDataDir, "TestTiddler.tid"), osReader}, dummyAsTid, false},
		// TODO: Add test case to test meta / binary tiddler pair
		// TODO: Add more test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readTiddlerFileWithReadCloser(tt.args.path, tt.args.reader)
			if (err != nil) != tt.wantErr {
				t.Errorf("readTiddlerFileWithReadCloser() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !areTiddlersEqual(t, tt.want, got) {
				t.Errorf("returned tiddler does not match expected")
			}
		})
	}
}

// focused on testing that it reads from the cache when available
func Test_getTiddlerFileFromStore(t *testing.T) {
	dummyAsTid := getTestTiddlerJsonAsTid(t, "TestTiddler.json")
	dummyReader := func(t *testing.T, name string) func(path string) (io.ReadCloser, error) {
		tid := getTestTiddler(t, name)
		return func(path string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(string(tid))), nil
		}
	}
	type args struct {
		title       string
		tiddlersDir string
		index       map[string]string
		cache       map[string]Tiddler
		reader      func(path string) (io.ReadCloser, error)
	}
	tests := []struct {
		name    string
		args    args
		want    Tiddler
		wantErr bool
	}{
		{"standard tiddler from store",
			args{dummyAsTid["title"].(string), "dummyTiddlersDir",
				make(map[string]string), make(map[string]Tiddler), dummyReader(t, "TestTiddler.tid")},
			dummyAsTid, false},
		// TODO: Add test case to verify that tiddler is being retrieved from cache
		// TODO: Add test case to check unindexed
		// TODO: Add more test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantPath := filepath.Join(tt.args.tiddlersDir, tiddlerFilename(tt.args.title))
			gotTid, err := getTiddlerFileFromStore(tt.args.title, tt.args.tiddlersDir, tt.args.index, tt.args.cache,
				func(path string) (io.ReadCloser, error) {
					if wantPath != path {
						t.Errorf("getTiddlerFileFromStore() path does not match expected = %s, want %s", wantPath, path)
					}
					return tt.args.reader(path)
				})
			if (err != nil) != tt.wantErr {
				t.Errorf("getTiddlerFileFromStore() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !areTiddlersEqual(t, dummyAsTid, gotTid) {
				t.Errorf("getTiddlerFileFromStore(): returned tiddler does not match expected")
			}
		})
	}
}

func Test_getAllTiddlerFilesFromStore(t *testing.T) {
	dummyAsTid := getTestTiddlerJsonAsTid(t, "TestTiddler.json")
	readerShouldNotBeCalled := func(string) (io.ReadCloser, error) {
		t.Fatal("walker called even though the cache was full")
		return nil, nil
	}
	walkerShouldNotBeCalled := func(func(string) error) error {
		t.Fatal("walker called even though the cache was full")
		return nil
	}
	standardTiddlersCache := map[string]Tiddler{dummyAsTid["title"].(string): dummyAsTid}
	type args struct {
		cache  map[string]Tiddler
		reader func(string) (io.ReadCloser, error)
		walker func(func(string) error) error
	}
	tests := []struct {
		name    string
		args    args
		want    []Tiddler
		wantErr bool
	}{
		{"standard tiddlers, in cache",
			args{standardTiddlersCache,
				readerShouldNotBeCalled,
				walkerShouldNotBeCalled},
			[]Tiddler{dummyAsTid}, false},
		{"single, standard tiddler; no cache",
			args{map[string]Tiddler{},
				func(path string) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader(string(getTestTiddler(t, path)))), nil
				},
				func(f func(string) error) error {
					return f("TestTiddler.tid")
				}},
			[]Tiddler{dummyAsTid}, false},
		// TODO: test multiple tiddlers
		// TODO: test failed walker or reader?
		// TODO: add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getAllTiddlerFilesFromStore(tt.args.cache, tt.args.reader, tt.args.walker)
			// TODO: verify the paths?
			if (err != nil) != tt.wantErr {
				t.Errorf("getAllTiddlerFilesFromStore() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !areTiddlerSlicesEqual(t, tt.want, got) {
				t.Errorf("getAllTiddlerFilesFromStore(): returned tiddler slice does not match expected")
			}
		})
	}
}

func Test_buildCacheAndIndex(t *testing.T) {
	dummyAsTid := getTestTiddlerJsonAsTid(t, "TestTiddler.json")
	standardTiddlersIndex := map[string]string{dummyAsTid["title"].(string): "TestTiddler.tid"}
	// standardTiddlersRindex := map[string]string{"dummyPath": dummyAsTid["title"].(string)}
	standardTiddlersCache := map[string]Tiddler{dummyAsTid["title"].(string): dummyAsTid}
	readerStandardTiddlers := func(path string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(string(getTestTiddler(t, path)))), nil
	}
	walkerStandardTiddlers := func(f func(path string) error) error {
		for _, path := range standardTiddlersIndex {
			if err := f(path); err != nil {
				return err
			}
		}
		return nil
	}
	type args struct {
		walker func(f func(path string) error) error
		reader func(path string) (io.ReadCloser, error)
	}
	tests := []struct {
		name      string
		args      args
		wantIndex map[string]string
		wantCache map[string]Tiddler
		wantErr   bool
	}{
		{"standard tiddlers",
			args{walkerStandardTiddlers, readerStandardTiddlers},
			standardTiddlersIndex, standardTiddlersCache, false},
		// TODO: test multiple tiddlers
		// TODO: test failed walker or reader?
		// TODO: add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIndex, gotCache, err := buildCacheAndIndex(tt.args.walker, tt.args.reader)
			if (err != nil) != tt.wantErr {
				t.Errorf("buildCacheAndIndex() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotIndex, tt.wantIndex) {
				t.Errorf("buildCacheAndIndex() gotIndex = %v, wantIndex %v", gotIndex, tt.wantIndex)
			}
			if !tt.wantErr && !areTiddlerMapsEqual(t, tt.wantCache, gotCache) {
				t.Errorf("buildCacheAndIndex(): returned tiddler cache does not match expected")
			}
		})
	}
}
