package tiddlybucket

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestTiddler_Read(t *testing.T) {
	dummyJson := getTestTiddlerJson(t, "TestTiddler.json")
	dummyAsTid := getTestTiddlerJsonAsTid(t, "TestTiddler.json")

	type args struct {
		r io.Reader
	}
	tests := []struct {
		name    string
		tr      *Tiddler
		args    args
		wantErr bool
	}{
		{"nil reader", new(Tiddler), args{nil}, true},
		{"empty input", new(Tiddler), args{strings.NewReader("")}, true},
		{"good json", new(Tiddler), args{strings.NewReader(string(dummyJson))}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.tr.Read(tt.args.r); (err != nil) != tt.wantErr {
				t.Errorf("Tiddler.Read() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !areTiddlersEqual(t, dummyAsTid, *tt.tr) {
				t.Errorf("returned tiddler does not match expected")
			}
		})
	}
}

func TestTiddlerFile_Read(t *testing.T) {
	dummy := getTestTiddler(t, "TestTiddler.tid")
	dummyAsTid := getTestTiddlerJsonAsTid(t, "TestTiddler.json")

	type fields struct {
		tid Tiddler
	}
	type args struct {
		r io.Reader
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    Tiddler
		wantErr bool
	}{
		{"nil reader", fields{}, args{nil}, nil, true},
		{"empty input", fields{}, args{strings.NewReader("")}, emptyTiddlerFile, false},
		{"good dummy", fields{}, args{strings.NewReader(string(dummy))}, dummyAsTid, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &TiddlerFile{
				tid: tt.fields.tid,
			}
			if err := tr.Read(tt.args.r); (err != nil) != tt.wantErr {
				t.Errorf("TiddlerFile.Read() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !areTiddlersEqual(t, tt.want, tr.tid) {
				t.Errorf("returned tiddler does not match expected")
			}
		})
	}
}

func TestTiddlerFile_Write(t *testing.T) {
	anotherTiddler := generateTiddler(t)
	dummyAsTid := getTestTiddlerJsonAsTid(t, "TestTiddler.json")
	// tiddlyWebFormat := getTestTiddlerJsonAsTid(t, "tiddlyweb-format.json")
	type fields struct {
		tid Tiddler
	}
	tests := []struct {
		name    string
		fields  fields
		want    Tiddler
		wantErr bool
	}{
		{"standard tiddler", fields{tid: dummyAsTid}, dummyAsTid, false},
		{"rando tiddler", fields{tid: anotherTiddler}, anotherTiddler, false},
		// nope: {"tiddlyweb formatted tiddler", fields{tid: tiddlyWebFormat}, tiddlyWebFormat, false},
		// TODO: add more test cases
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &TiddlerFile{
				tid: tt.fields.tid,
			}
			// t.Log(tr.tid)
			w := &bytes.Buffer{}
			if err := tr.Write(w); (err != nil) != tt.wantErr {
				t.Errorf("TiddlerFile.Write() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			gotW := w.String()
			var gotTid TiddlerFile
			if err := gotTid.Read(strings.NewReader(gotW)); err != nil {
				t.Logf("gotW: %s\n", gotW)
				t.Errorf("unable to use TiddlerFile.Read() to load the output of TiddlerFile.Write(): %v", err)
				return
			}
			if !tt.wantErr && !areTiddlersEqual(t, tt.want, gotTid.Tiddler()) {
				t.Errorf("returned tiddler does not match expected")
			}
		})
	}
}

func Test_getTiddlerFileTile(t *testing.T) {
	type args struct {
		f io.Reader
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{"nil reader", args{nil}, "", true},
		{"empty input", args{strings.NewReader("")}, "", true},
		{"good dummy", args{strings.NewReader(string(getTestTiddler(t, "TestTiddler.tid")))}, "TestTiddler", false},
		{"good but no title", args{strings.NewReader(string(getTestTiddler(t, "NoTitleTiddler.tid")))}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getTiddlerFileTile(tt.args.f)
			if (err != nil) != tt.wantErr {
				t.Errorf("getTiddlerFileTile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getTiddlerFileTile() = %v, want %v", got, tt.want)
			}
		})
	}
}
