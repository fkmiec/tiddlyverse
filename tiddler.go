package tiddlybucket

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
)

var (
	reWhitespaceOnly     = regexp.MustCompile(`^\s*$`)
	reMatchMultiWordTags = regexp.MustCompile(`\[\[[^]]*\]\]`)
)

type Tiddler map[string]interface{}

func (t *Tiddler) Field(name string) string {
	if v, ok := (*t)[name]; ok {
		return v.(string)
	}
	return ""
}

func (t *Tiddler) setField(name, value string) {
	(*t)[name] = value
}

func (t *Tiddler) Bytes() []byte {
	if b, err := json.Marshal(t); err == nil {
		return b
	}
	return nil
}

func (t *Tiddler) Read(r io.Reader) error {
	if r == nil {
		return fmt.Errorf("reader is nil")
	}
	if err := json.NewDecoder(r).Decode(t); err != nil {
		return fmt.Errorf("could not read JSON as tiddler: %s", err.Error())
	}
	// TODO: what about revision here?
	return nil
}

type TiddlerFile struct {
	tid Tiddler
}

func (t *TiddlerFile) Tiddler() Tiddler {
	return t.tid
}

func (t *TiddlerFile) Write(w io.Writer) error {
	if w == nil {
		return fmt.Errorf("writer is nil")
	}

	var buf bytes.Buffer

	for f, v := range t.tid {
		switch f {
		/*
			case "tags":
				buf.WriteString(f)
				buf.WriteString(": ")
				for _, tag := range v.([]interface{}) {
					numWords := len(strings.Split(tag.(string), " "))
					if numWords > 1 {
						buf.WriteString(fmt.Sprintf("[[%s]]", tag.(string)))
					} else {
						buf.WriteString(tag.(string))
					}
					// buf.WriteString(tag.(string))
					buf.WriteByte(' ')
				}
				buf.WriteByte('\n')
		*/
		case "fields":
			for f2, v2 := range v.(map[string]interface{}) {
				buf.WriteString(f2)
				buf.WriteString(": ")
				buf.WriteString(v2.(string))
				buf.WriteByte('\n')
			}
		case "text":
			continue
		default:
			buf.WriteString(f)
			buf.WriteString(": ")
			buf.WriteString(v.(string))
			buf.WriteByte('\n')
		}
	}

	buf.WriteByte('\n') // needs to have a newline separator

	if txt, ok := t.tid["text"]; ok {
		buf.WriteString(txt.(string))
	}

	_, err := w.Write(buf.Bytes())
	return err
}

// TODO?: https://github.com/Jermolene/TiddlyWiki5/blob/master/plugins/tiddlywiki/tiddlyweb/tiddlywebadaptor.js#L271
func (t *TiddlerFile) Read(r io.Reader) error {
	if r == nil {
		return fmt.Errorf("reader is nil")
	}

	t.tid = make(map[string]interface{})
	fields := make(map[string]interface{})

	reader := bufio.NewReader(r)
	for {
		line, rerr := reader.ReadString('\n')
		fmt.Println("DEBUG: " + line)
		if rerr != nil && rerr != io.EOF {
			log.Error().Err(rerr).Msg("error reading tiddler line")
			// TODO: make better error handling
		}
		if reWhitespaceOnly.MatchString(line) {
			b, err := io.ReadAll(reader)
			if err != nil {
				log.Error().Err(err).Msg("could not read the tiddler body")
				// TODO: make better error handling
			}
			if len(b) > 0 {
				t.tid["text"] = string(b)
			}
			break
		}
		idx := strings.Index(line, ":")
		name := line[:idx]
		value := strings.TrimSpace(line[idx+1:])
		/*
			switch name {
			case "tags":
				/// well, this is hideous
				if len(value) > 0 {
					value = reMatchMultiWordTags.ReplaceAllStringFunc(value,
						func(str string) string {
							return strings.ReplaceAll(str, " ", "~tb~")
						})
					tagsStrArr := strings.Split(value, " ")
					tagsArr := make([]interface{}, len(tagsStrArr))
					for i, t := range tagsStrArr {
						tagsArr[i] = strings.TrimSuffix(strings.TrimPrefix(strings.ReplaceAll(t, "~tb~", " "), "[["), "]]")
					}
					t.tid[name] = tagsArr
				}
			default:
				t.tid[name] = value
			}
		*/
		t.tid[name] = value

		if rerr == io.EOF {
			break
		}
	}
	if len(fields) > 0 {
		t.tid["fields"] = fields
	}

	// TiddlyWiki5 <= 5.2.3 syncer is expecting the revision field to exist so it does not accept "old" and "new" revisions to be both 'undefined'
	// https://github.com/Jermolene/TiddlyWiki5/blob/master/core/modules/syncer.js#L351-L354
	// https://github.com/Jermolene/TiddlyWiki5/blob/master/core/modules/syncer.js#L360
	if _, ok := t.tid["revision"]; !ok {
		t.tid["revision"] = "0"
	}

	return nil
}

func getTiddlerFileTile(f io.Reader) (string, error) {
	if f == nil {
		return "", fmt.Errorf("reader is nil")
	}
	r := bufio.NewReader(f)
	for {
		line, rerr := r.ReadString('\n')
		if rerr != nil && rerr != io.EOF {
			return "", fmt.Errorf("error reading tiddler line: %s", rerr)
		}
		if reWhitespaceOnly.MatchString(line) {
			break
		}
		if strings.HasPrefix(line, "title:") {
			title := strings.TrimSpace(line[strings.Index(line, ":")+1:])
			if title == "" {
				return "", fmt.Errorf("")
			}
			return title, nil
		}
		if rerr == io.EOF {
			break
		}
	}
	return "", fmt.Errorf("title not found")
}
