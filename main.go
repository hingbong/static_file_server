package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"mime"
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
<header>
    <form method="post" action="" enctype="multipart/form-data">
        <input type="file" name="upload">
        <input type="submit" value="upload"/>
    </form>
</header>
<article>
    <table>
        <tr>
            <td>file</td>
            <td>mod time</td>
            <td>size</td>
            <td>is dir</td>
        </tr>
        <tr><td><a href="{{.Location}}/..">..</a></td></tr>
        {{range .Files}}
            <tr>
                <td><a href="{{$.Location}}/{{.Name}}">{{.Name}}</a></td>
                <td>{{.ModTime}}</td>
                <td>{{.Size}}</td>
                <td>{{.IsDir}}</td>
            </tr>
        {{end}}
    </table>
</article>
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
		requestURI = fmt.Sprintf(".%s", strings.TrimRight(requestURI, "/"))

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
		_, _ = writer.Write([]byte(fmt.Sprintf("<h1>%s NOT FOUND<h1>", requestURI)))
		log.Println(requestURI, "NOT FOUND")
		return
	}
	switch mode := uri.Mode(); {
	case mode.IsDir():
		files, err := ioutil.ReadDir(requestURI)
		if err != nil {
			log.Println(err)
			return
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
			log.Println(err)
		}
		buffer := bytes.Buffer{}
		err = parse.Execute(&buffer, p)
		if err != nil {
			log.Println(err)
			return
		}
		writer.Header().Set("Content-Length", strconv.Itoa(buffer.Len()))
		_, err = writer.Write(buffer.Bytes())
		if err != nil {
			log.Println(err)
			return
		}
	case mode.IsRegular():
		file, e := os.Open(requestURI)
		if e != nil {
			log.Println(e)
			return
		}
		defer file.Close()
		reader := bufio.NewReader(file)
		index := strings.LastIndex(file.Name(), ".")
		var mimeTp string
		if index > -1 {
			mimeTp = mime.TypeByExtension(file.Name()[index:])
		}
		if mimeTp == "" {
			writer.Header().Set("Content-Type", "application/octet-stream")
		} else {
			writer.Header().Set("Content-Type", mimeTp)
		}
		fileInfo, e := file.Stat()
		if e != nil {
			log.Println(e)
		}
		writer.Header().Set("Content-Length", strconv.Itoa(int(fileInfo.Size())))
		_, e = reader.WriteTo(writer)
		if e != nil {
			log.Println(e)
		}
	}
}

func doPost(writer http.ResponseWriter, request *http.Request, requestURI string) {
	_ = request.ParseMultipartForm(1<<63 - 1)
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
