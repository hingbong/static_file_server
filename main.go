package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/net/http2"
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
    <link rel="icon" href="favicon.svg" type="image/svg+xml">
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

var enableSsl = flag.Bool("ssl", true, "enable SSL")
var port = flag.Int("port", 443, "指定所监听端口,默认443")
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
		uri := request.URL.RequestURI()
		index := strings.Index(uri, "?")
		if index >= 0 {
			uri = uri[:index]
		}
		requestURI, err := url.PathUnescape(uri)
		handleError(err, writer)
		log.Println(request.Proto, request.RemoteAddr, request.Method, requestURI)
		requestURI = fmt.Sprintf(".%s", strings.TrimSuffix(requestURI, "/"))

		switch request.Method {
		case http.MethodGet:
			if requestURI == "./favicon.svg" {
				writer.Header().Set("content-type", "image/svg+xml")
				writer.Write(iconData)
				return
			}
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
	var srv http.Server
	srv.TLSConfig = tlsConfig()
	_ = http2.ConfigureServer(&srv, &http2.Server{})
	p := strconv.Itoa(*port)

	srv.Addr = ":" + p
	go func() {
		if !*enableSsl {
			log.Fatal(srv.ListenAndServe())
		} else {
			log.Fatal(srv.ListenAndServeTLS("", ""))
		}
	}()
	select {}
}

func doGet(writer http.ResponseWriter, requestURI string) error {
	uri, e := os.Stat(requestURI)
	if e != nil {
		if os.IsNotExist(e) {
			return fileNotFound(writer, requestURI)
		}
		return e
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
	parent := (&url.URL{Path: requestURI}).String()
	_, err = fmt.Fprintf(&buffer, htmlFirstPart, name, parent)
	if err != nil {
		return err
	}
	for _, v := range files {
		_, err := fmt.Fprintf(&buffer, htmlTableRow, parent, (&url.URL{Path: v.Name()}).String(), htmlReplacer.Replace(v.Name()), v.ModTime(), strconv.Itoa(int(v.Size())), strconv.FormatBool(v.IsDir()))
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
		mimeType := mime.TypeByExtension(file.Name()[index:])
		if len(strings.TrimSpace(mimeType)) == 0 {
			mimeType = "text/plain; charset=utf-8"
		}
		writer.Header().Set("Content-Type", mimeType)
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

func tlsConfig() *tls.Config {
	crt := `-----BEGIN CERTIFICATE-----
MIIDPjCCAiYCCQDizia/MoUFnDANBgkqhkiG9w0BAQUFADB7MQswCQYDVQQGEwJV
UzELMAkGA1UECBMCQ0ExFjAUBgNVBAcTDVNhbiBGcmFuY2lzY28xFDASBgNVBAoT
C0JyYWRmaXR6aW5jMRIwEAYDVQQDEwlsb2NhbGhvc3QxHTAbBgkqhkiG9w0BCQEW
DmJyYWRAZGFuZ2EuY29tMB4XDTE0MDcxNTIwNTAyN1oXDTE1MTEyNzIwNTAyN1ow
RzELMAkGA1UEBhMCVVMxCzAJBgNVBAgTAkNBMQswCQYDVQQHEwJTRjEeMBwGA1UE
ChMVYnJhZGZpdHogaHR0cDIgc2VydmVyMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A
MIIBCgKCAQEAs1Y9CyLFrdL8VQWN1WaifDqaZFnoqjHhCMlc1TfG2zA+InDifx2l
gZD3o8FeNnAcfM2sPlk3+ZleOYw9P/CklFVDlvqmpCv9ss/BEp/dDaWvy1LmJ4c2
dbQJfmTxn7CV1H3TsVJvKdwFmdoABb41NoBp6+NNO7OtDyhbIMiCI0pL3Nefb3HL
A7hIMo3DYbORTtJLTIH9W8YKrEWL0lwHLrYFx/UdutZnv+HjdmO6vCN4na55mjws
/vjKQUmc7xeY7Xe20xDEG2oDKVkL2eD7FfyrYMS3rO1ExP2KSqlXYG/1S9I/fz88
F0GK7HX55b5WjZCl2J3ERVdnv/0MQv+sYQIDAQABMA0GCSqGSIb3DQEBBQUAA4IB
AQC0zL+n/YpRZOdulSu9tS8FxrstXqGWoxfe+vIUgqfMZ5+0MkjJ/vW0FqlLDl2R
rn4XaR3e7FmWkwdDVbq/UB6lPmoAaFkCgh9/5oapMaclNVNnfF3fjCJfRr+qj/iD
EmJStTIN0ZuUjAlpiACmfnpEU55PafT5Zx+i1yE4FGjw8bJpFoyD4Hnm54nGjX19
KeCuvcYFUPnBm3lcL0FalF2AjqV02WTHYNQk7YF/oeO7NKBoEgvGvKG3x+xaOeBI
dwvdq175ZsGul30h+QjrRlXhH/twcuaT3GSdoysDl9cCYE8f1Mk8PD6gan3uBCJU
90p6/CbU71bGbfpM2PHot2fm
-----END CERTIFICATE-----`

	key := `-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEAs1Y9CyLFrdL8VQWN1WaifDqaZFnoqjHhCMlc1TfG2zA+InDi
fx2lgZD3o8FeNnAcfM2sPlk3+ZleOYw9P/CklFVDlvqmpCv9ss/BEp/dDaWvy1Lm
J4c2dbQJfmTxn7CV1H3TsVJvKdwFmdoABb41NoBp6+NNO7OtDyhbIMiCI0pL3Nef
b3HLA7hIMo3DYbORTtJLTIH9W8YKrEWL0lwHLrYFx/UdutZnv+HjdmO6vCN4na55
mjws/vjKQUmc7xeY7Xe20xDEG2oDKVkL2eD7FfyrYMS3rO1ExP2KSqlXYG/1S9I/
fz88F0GK7HX55b5WjZCl2J3ERVdnv/0MQv+sYQIDAQABAoIBADQ2spUwbY+bcz4p
3M66ECrNQTBggP40gYl2XyHxGGOu2xhZ94f9ELf1hjRWU2DUKWco1rJcdZClV6q3
qwmXvcM2Q/SMS8JW0ImkNVl/0/NqPxGatEnj8zY30d/L8hGFb0orzFu/XYA5gCP4
NbN2WrXgk3ZLeqwcNxHHtSiJWGJ/fPyeDWAu/apy75u9Xf2GlzBZmV6HYD9EfK80
LTlI60f5FO487CrJnboL7ovPJrIHn+k05xRQqwma4orpz932rTXnTjs9Lg6KtbQN
a7PrqfAntIISgr11a66Mng3IYH1lYqJsWJJwX/xHT4WLEy0EH4/0+PfYemJekz2+
Co62drECgYEA6O9zVJZXrLSDsIi54cfxA7nEZWm5CAtkYWeAHa4EJ+IlZ7gIf9sL
W8oFcEfFGpvwVqWZ+AsQ70dsjXAv3zXaG0tmg9FtqWp7pzRSMPidifZcQwWkKeTO
gJnFmnVyed8h6GfjTEu4gxo1/S5U0V+mYSha01z5NTnN6ltKx1Or3b0CgYEAxRgm
S30nZxnyg/V7ys61AZhst1DG2tkZXEMcA7dYhabMoXPJAP/EfhlWwpWYYUs/u0gS
Wwmf5IivX5TlYScgmkvb/NYz0u4ZmOXkLTnLPtdKKFXhjXJcHjUP67jYmOxNlJLp
V4vLRnFxTpffAV+OszzRxsXX6fvruwZBANYJeXUCgYBVouLFsFgfWGYp2rpr9XP4
KK25kvrBqF6JKOIDB1zjxNJ3pUMKrl8oqccCFoCyXa4oTM2kUX0yWxHfleUjrMq4
yimwQKiOZmV7fVLSSjSw6e/VfBd0h3gb82ygcplZkN0IclkwTY5SNKqwn/3y07V5
drqdhkrgdJXtmQ6O5YYECQKBgATERcDToQ1USlI4sKrB/wyv1AlG8dg/IebiVJ4e
ZAyvcQmClFzq0qS+FiQUnB/WQw9TeeYrwGs1hxBHuJh16srwhLyDrbMvQP06qh8R
48F8UXXSRec22dV9MQphaROhu2qZdv1AC0WD3tqov6L33aqmEOi+xi8JgbT/PLk5
c/c1AoGBAI1A/02ryksW6/wc7/6SP2M2rTy4m1sD/GnrTc67EHnRcVBdKO6qH2RY
nqC8YcveC2ZghgPTDsA3VGuzuBXpwY6wTyV99q6jxQJ6/xcrD9/NUG6Uwv/xfCxl
IJLeBYEqQundSSny3VtaAUK8Ul1nxpTvVRNwtcyWTo8RHAAyNPWd
-----END RSA PRIVATE KEY-----`

	cert, err := tls.X509KeyPair([]byte(crt), []byte(key))
	if err != nil {
		log.Fatal(err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		VerifyConnection: func(state tls.ConnectionState) error {
			return nil
		},
		VerifyPeerCertificate: nil,
	}
}

var iconData = []byte(icon)

const icon = `<?xml version="1.0" encoding="iso-8859-1"?>
<!-- Generator: Adobe Illustrator 18.1.1, SVG Export Plug-In . SVG Version: 6.00 Build 0)  -->
<svg version="1.1" id="Capa_1" xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" x="0px" y="0px"
	 viewBox="0 0 29.535 29.535" style="enable-background:new 0 0 29.535 29.535;" xml:space="preserve">
<g>
	<path d="M14.768,0C6.611,0,0,6.609,0,14.768c0,8.155,6.611,14.767,14.768,14.767c8.154,0,14.766-6.612,14.766-14.767
		C29.534,6.609,22.923,0,14.768,0z M14.768,27.126c-6.83,0-12.361-5.532-12.361-12.359c0-6.828,5.531-12.362,12.361-12.362
		c6.824,0,12.359,5.535,12.359,12.362C27.128,21.594,21.592,27.126,14.768,27.126z"/>
	<polygon points="16.83,11.143 12.679,11.143 12.679,11.15 11.134,11.15 11.134,13.563 12.679,13.563 12.679,22.181 11.039,22.181 
		11.039,24.487 12.679,24.487 12.679,24.503 16.83,24.503 16.83,24.487 18.188,24.487 18.188,22.181 16.83,22.181 	"/>
	<path d="M14.726,9.504c1.395,0,2.24-0.928,2.24-2.077c-0.027-1.172-0.846-2.072-2.184-2.072c-1.336,0-2.211,0.899-2.211,2.072
		C12.57,8.576,13.417,9.504,14.726,9.504z"/>
	<g>
	</g>
	<g>
	</g>
	<g>
	</g>
	<g>
	</g>
	<g>
	</g>
	<g>
	</g>
	<g>
	</g>
	<g>
	</g>
	<g>
	</g>
	<g>
	</g>
	<g>
	</g>
	<g>
	</g>
	<g>
	</g>
	<g>
	</g>
	<g>
	</g>
</g>
<g>
</g>
<g>
</g>
<g>
</g>
<g>
</g>
<g>
</g>
<g>
</g>
<g>
</g>
<g>
</g>
<g>
</g>
<g>
</g>
<g>
</g>
<g>
</g>
<g>
</g>
<g>
</g>
<g>
</g>
</svg>

`
