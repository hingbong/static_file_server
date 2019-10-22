package main

import (
	"bufio"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strconv"
)

const htmlCode = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>{{.Title}}</title>
    <base href="/">
</head>
<body>
<form method="post" action="" enctype="multipart/form-data">
    <input type="file" name="upload">
    <input type="submit" value="upload"/>
</form>
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

const sep = "/"

type page struct {
	Title    string
	Files    []os.FileInfo
	Location string
}

var port = flag.Int("port", 80, "指定所监听端口,默认80")
var path = flag.String("dir", ".", "指定工作目录,默认当前目录")

func main() {
	flag.PrintDefaults()
	flag.Parse()
	e := os.Chdir(*path)
	if e != nil {
		log.Fatalln(e)
	}
	runtime.GOMAXPROCS(8)
	http.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		requestURI, _ := url.PathUnescape(request.URL.RequestURI())
		fmt.Println(request.RemoteAddr, request.Method, requestURI)
		requestURI = fmt.Sprintf(".%s", requestURI)

		switch request.Method {
		case http.MethodGet:
			doGet(writer, requestURI)
		case http.MethodPost:
			doPost(writer, request, requestURI)
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	p := strconv.Itoa(*port)
	e = http.ListenAndServe(":"+p, nil)
	if e != nil {
		log.Fatalln(e)
	}
}

func doGet(writer http.ResponseWriter, requestURI string) {

	uri, e := os.Stat(requestURI)
	if os.IsNotExist(e) {
		writer.WriteHeader(404)
		log.Println(requestURI, "NOT FOUND")
		return
	}
	switch mode := uri.Mode(); {
	case mode.IsDir():
		files, err := ioutil.ReadDir(requestURI)
		if err != nil {
			log.Panicln(err)
		}
		fs, dirs := splitFileAndDir(files)
		sort.SliceStable(fs, func(i, j int) bool {
			return fs[i].Name() <= fs[j].Name()
		})
		sort.SliceStable(dirs, func(i, j int) bool {
			return dirs[i].Name() <= dirs[j].Name()
		})

		p := &page{
			Title:    requestURI,
			Files:    append(dirs, fs...),
			Location: requestURI,
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
		file, e := os.Open(requestURI)
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
}

func doPost(writer http.ResponseWriter, request *http.Request, requestURI string) {
	_ = request.ParseMultipartForm(32 << 20)
	file, handler, err := request.FormFile("upload")
	if err != nil {
		_, _ = writer.Write([]byte(err.Error()))
		log.Println(err)
		return
	}
	defer file.Close()
	filename := requestURI + sep + handler.Filename
	info, _ := os.Stat(filename)
	if info != nil {
		_, _ = writer.Write([]byte("<h1>The File Is Existed</h1>"))
		log.Println(filename + ": The File Is Existed")
		return
	}
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		log.Println(err)
		_, _ = writer.Write([]byte(err.Error()))
		return
	}
	defer f.Close()
	_, err = io.Copy(f, file)
	if err != nil {
		_, _ = writer.Write([]byte(err.Error()))
		log.Println(err)
		return
	}
	doGet(writer, requestURI)
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
