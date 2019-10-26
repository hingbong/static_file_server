package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
)

const htmlFirstPart = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>%s</title>
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
        <tr><td><a href="%s/..">..</a></td></tr>`

const htmlTableRow = `<tr>
                <td><a href="%s/%s">%s</a></td>
                <td>%s</td>
                <td>%s</td>
                <td>%s</td>
            </tr>`

const htmlLastPart = `</table>
</article>
</body>
</html>`

var htmlReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	// "&#34;" is shorter than "&quot;".
	`"`, "&#34;",
	// "&#39;" is shorter than "&apos;" and apos was not in HTML until HTML5.
	"'", "&#39;",
)

const sep = "/"

var port = flag.Int("port", 80, "指定所监听端口,默认80")
var path = flag.String("dir", ".", "指定工作目录,默认当前目录")

func init() {
	flag.PrintDefaults()
	flag.Parse()
}

func main() {
	e := os.Chdir(*path)
	if e != nil {
		log.Fatalln(e)
	}
	http.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		requestURI, _ := url.PathUnescape(request.URL.RequestURI())
		log.Println(request.RemoteAddr, request.Method, requestURI)
		requestURI = fmt.Sprintf(".%s", strings.TrimRight(requestURI, "/"))

		switch request.Method {
		case http.MethodGet:
			err := doGet(writer, requestURI)
			handleError(err, writer)
		case http.MethodPost:
			err := doPost(request, requestURI)
			if handleError(err, writer) {
				return
			}
			log.Printf("%s uploaded", requestURI)
			err = directoryProcess(requestURI, writer)
			handleError(err, writer)
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

func doGet(writer http.ResponseWriter, requestURI string) error {
	uri, e := os.Stat(requestURI)
	if os.IsNotExist(e) {
		return fileNotFound(writer, requestURI)
	}
	switch mode := uri.Mode(); {
	case mode.IsDir():
		e = directoryProcess(requestURI, writer)
	case mode.IsRegular():
		e = filesProcess(requestURI, writer)
	default:
		e = fileNotFound(writer, requestURI)
	}
	return e
}

func fileNotFound(writer http.ResponseWriter, requestURI string) error {
	writer.WriteHeader(404)
	_, _ = fmt.Fprintf(writer, "<h1>%s NOT FOUND<h1>", requestURI)
	return errors.New(fmt.Sprintf("%s not found", requestURI))
}

func doPost(request *http.Request, requestURI string) error {
	e := request.ParseMultipartForm(1 << 10 << 10 << 10 * 10)
	if e != nil {
		return e
	}
	src, handler, e := request.FormFile("upload")
	if e != nil {
		return e
	}
	defer src.Close()
	filename := requestURI + sep + handler.Filename
	info, _ := os.Stat(filename)
	if info != nil {
		return fmt.Errorf("%s is exist", filename)
	}
	dst, e := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE, 0666)
	if e != nil {
		return e
	}
	defer dst.Close()
	_, e = bufio.NewReader(src).WriteTo(dst)
	return e
}

func directoryProcess(requestURI string, writer http.ResponseWriter) error {
	files, err := ioutil.ReadDir(requestURI)
	if err != nil {
		return err
	}
	fs, dirs := splitDirsAndFiles(files)
	sort.SliceStable(fs, func(i, j int) bool {
		return strings.ToUpper(fs[i].Name()) <= strings.ToUpper(fs[j].Name())
	})
	sort.SliceStable(dirs, func(i, j int) bool {
		return strings.ToUpper(dirs[i].Name()) <= strings.ToUpper(dirs[j].Name())
	})
	buffer := bytes.Buffer{}
	name := htmlReplacer.Replace(requestURI)
	parent := url.URL{Path: requestURI}
	_, err = fmt.Fprintf(&buffer, htmlFirstPart, name, parent.String())
	if err != nil {
		return err
	}
	for _, v := range files {
		fileUrl := url.URL{Path: v.Name()}
		_, err := fmt.Fprintf(&buffer, htmlTableRow, parent.String(), fileUrl.String(), htmlReplacer.Replace(v.Name()), v.ModTime(), strconv.Itoa(int(v.Size())), strconv.FormatBool(v.IsDir()))
		if err != nil {
			return err
		}
	}
	buffer.WriteString(htmlLastPart)
	writer.Header().Set("Content-Length", strconv.Itoa(buffer.Len()))
	_, err = writer.Write(buffer.Bytes())
	return err
}

func filesProcess(requestURI string, writer http.ResponseWriter) error {
	file, e := os.Open(requestURI)
	if e != nil {
		return e
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	index := strings.LastIndex(file.Name(), ".")
	if index > -1 {
		writer.Header().Set("Content-Type", mime.TypeByExtension(file.Name()[index:]))
	}
	fileInfo, e := file.Stat()
	if e != nil {
		return e
	}
	writer.Header().Set("Content-Length", strconv.Itoa(int(fileInfo.Size())))
	_, e = reader.WriteTo(writer)
	return e
}

// the first part is directories, and the second part is files
func splitDirsAndFiles(files []os.FileInfo) ([]os.FileInfo, []os.FileInfo) {
	var i int
	for k, v := range files {
		if v.IsDir() {
			files[i], files[k] = v, files[i]
			i++
		}
	}
	return files[:i], files[i:]
}

func handleError(err error, writer io.Writer) bool {
	if err != nil {
		_, _ = fmt.Fprintf(writer, err.Error())
		log.Println(err)
		return true
	}
	return false
}
