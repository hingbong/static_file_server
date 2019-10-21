package main

import (
	"bufio"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const htmlCode = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>{{.Title}}</title>
    <base href="/">
</head>
<body>
<table>
    <td>file</td>
    <td>mod time</td>
    <td>size</td>
    <td>is dir</td>
    {{range .Files}}
        <tr>
            <td><a href="{{$.Location}}/{{.Name}}">{{.Name}}</a></td>
            <td>{{.ModTime}}</td>
            <td>{{.Size}}</td>
            <td>{{.IsDir}}</td>
        </tr>{{end}}
</table>
</body>
</html>`

type page struct {
	Title    string
	Files    []os.FileInfo
	Location string
}

var port = flag.Int("port", 80, "指定所监听端口,默认80")
var path = flag.String("dir", ".", "指定工作目录,默认当前目录")

func main() {
	flag.PrintDefaults()
	e := os.Chdir(*path)
	if e != nil {
		log.Fatalln(e)
	}
	runtime.GOMAXPROCS(8)
	http.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		s, _ := url.PathUnescape(request.URL.RequestURI())
		URL := fmt.Sprintf(".%s", s)
		fmt.Println(request.RemoteAddr, request.Method, URL)
		uri, e := os.Stat(URL)
		if os.IsNotExist(e) {
			writer.WriteHeader(404)
			log.Println(URL, "NOT FOUND")
			return
		}
		switch mode := uri.Mode(); {
		case mode.IsDir():
			files, err := ioutil.ReadDir(URL)
			if err != nil {
				log.Panicln(err)
			}
			s = strings.TrimLeft(s, "/")
			fs, dirs := splitFileAndDir(files)
			sort.SliceStable(fs, func(i, j int) bool {
				return fs[i].Name() <= fs[j].Name()
			})
			sort.SliceStable(dirs, func(i, j int) bool {
				return dirs[i].Name() <= dirs[j].Name()
			})
			p := &page{
				Title:    s,
				Files:    append(dirs, fs...),
				Location: s,
			}
			temp := template.New("Files List")
			parse, err := temp.Parse(htmlCode)
			if err != nil {
				fmt.Println(err)
			}
			err = parse.Execute(writer, p)
			if err != nil {
				fmt.Println(err)
			}
		case mode.IsRegular():
			file, e := os.Open(URL)
			if e != nil {
				log.Panicln(e)
			}
			defer file.Close()
			reader := bufio.NewReader(file)
			_, e = reader.WriteTo(writer)
			if e != nil {
				log.Println(e)
			}
		}

	})
	p := strconv.Itoa(*port)
	e = http.ListenAndServe(":"+p, nil)
	if e != nil {
		log.Fatalln(e)
	}
}

func splitFileAndDir(files []os.FileInfo) ([]os.FileInfo, []os.FileInfo) {
	dirs := make([]os.FileInfo, 0)
	fs := make([]os.FileInfo, 0)
	for _, v := range files {
		if v.IsDir() {
			dirs = append(dirs, v)
		} else {
			fs = append(fs, v)
		}
	}
	return fs, dirs
}
