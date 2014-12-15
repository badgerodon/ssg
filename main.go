package main

import (
	"encoding/base64"
	"github.com/howeyc/fsnotify"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var index = `<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta http-equiv="X-UA-Compatible" content="IE=edge,chrome=1">
    <meta name="viewport" content="width=device-width,initial-scale=1">
    <title></title>
    <link rel="stylesheet" href="index.css" >
  </head>
  <body>
    <script src="index.js"></script>
    <script>require("main")</script>
  </body>
</html>`

var jsheader = `(function() {
var registered = {},
	cache = {};

var expand = function(root, name) {
	var results = [], parts, part;
	if (/^\.\.?(\/|$)/.test(name)) {
		parts = [root, name].join('/').split('/');
	} else {
		parts = name.split('/');
	}
	for (var i = 0, length = parts.length; i < length; i++) {
		part = parts[i];
		if (part === '..') {
			results.pop();
		} else if (part !== '.' && part !== '') {
			results.push(part);
		}
	}
	return results.join('/');
};

var dirname = function(path) {
	return path.split('/').slice(0, -1).join('/');
};

var localRequire = function(path) {
	return function(name) {
		var dir = dirname(path);
		var absolute = expand(dir, name);
		return require(absolute, path);
	};
};

var module = function(name) {
	if (!registered[name]) {
		throw (name + " not found");
	}
	var m = { id: name, exports: {} };
	registered[name](m.exports, localRequire(name), m);
	return m;
};

window.global = window;
window.require = function(name) {
	cache[name] = cache[name] || module(name);
	return cache[name].exports;
};
window.require.register = function(name, definition) {
	registered[name] = definition;
};

})();
`

func generateJS(w io.Writer, c chan []string) error {
	readers := []io.Reader{}

	vendorRoot := filepath.Join("vendor", "scripts")
	vendorFiles := []string{}
	filepath.Walk(vendorRoot, func(p string, fi os.FileInfo, err error) error {
		if filepath.Ext(p) == ".js" {
			vendorFiles = append(vendorFiles, p)
		}
		return nil
	})

	root := filepath.Join("app", "scripts")
	files := []string{}
	filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if filepath.Ext(p) == ".js" {
			files = append(files, p)
		}
		return nil
	})

	if c != nil {
		allFiles := make([]string, 0, len(files)+len(vendorFiles))
		allFiles = append(allFiles, vendorFiles...)
		allFiles = append(allFiles, files...)
		c <- allFiles
	}

	for _, fn := range vendorFiles {
		fh, err := os.Open(fn)
		if err != nil {
			return err
		}
		defer fh.Close()
		readers = append(readers, fh)
		readers = append(readers, strings.NewReader("\n"))
	}

	readers = append(readers, strings.NewReader(jsheader))

	for _, fn := range files {
		fh, err := os.Open(fn)
		if err != nil {
			return err
		}
		defer fh.Close()
		moduleName := strings.Replace(fn[len(root)+1:], "\\", "/", -1)
		moduleName = moduleName[:len(moduleName)-3]

		pr, pw := io.Pipe()
		go func() {
			defer pw.Close()
			enc := base64.NewEncoder(base64.StdEncoding, pw)
			defer enc.Close()
			io.Copy(enc, fh)
			io.Copy(enc, strings.NewReader("\n//# sourceURL="+moduleName+".js"))
		}()

		readers = append(readers, strings.NewReader("require.register(\""+moduleName+"\", function(exports, require, module) {\neval(decodeURIComponent(escape(atob(\""))
		readers = append(readers, pr)
		readers = append(readers, strings.NewReader("\"))));\n});\n"))
	}
	_, err := io.Copy(w, io.MultiReader(readers...))
	return err
}

func generateCSS(w io.Writer) error {
	readers := []io.Reader{}

	files := []string{}
	root := filepath.Join("app", "styles")
	filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if filepath.Ext(p) == ".css" {
			files = append(files, p)
		}
		return nil
	})
	for _, fn := range files {
		fh, err := os.Open(fn)
		if err != nil {
			return err
		}
		defer fh.Close()

		readers = append(readers, fh)
		readers = append(readers, strings.NewReader("\n"))
	}
	_, err := io.Copy(w, io.MultiReader(readers...))
	return err
}

func build() error {
	log.Println("building compiled application")
	os.MkdirAll("public", 0666)
	err := ioutil.WriteFile("public/index.html", []byte(index), 0666)
	if err != nil {
		return err
	}
	jsw, err := os.Create("public/index.js")
	if err != nil {
		return err
	}
	defer jsw.Close()
	err = generateJS(jsw, nil)
	if err != nil {
		return err
	}
	cssw, err := os.Create("public/index.css")
	if err != nil {
		return err
	}
	defer cssw.Close()
	err = generateCSS(cssw)
	if err != nil {
		return err
	}
	return nil
}

func watch() error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	jsc := make(chan []string)
	cssc := make(chan []string)
	go func() {
		for {
			select {
			case jsFiles := <-jsc:
				log.Println("JS FILES:", jsFiles)
			case cssFiles := <-cssc:
				log.Println("CSS FILES:", cssFiles)
			}
		}
	}()

	log.Println("starting server on :3333")
	return http.ListenAndServe(":3333", http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/index.js":
			res.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			err := generateJS(res, jsc)
			if err != nil {
				http.Error(res, err.Error(), 500)
			}
		case "/index.css":
			res.Header().Set("Content-Type", "text/css; charset=utf-8")
			err := generateCSS(res)
			if err != nil {
				http.Error(res, err.Error(), 500)
			}
		case "/":
			res.Header().Set("Content-Type", "text/html")
			io.WriteString(res, index)
		default:
			res.Header().Set("Content-Type", "text/plain")
			http.Error(res, "not found", 404)
		}
	}))
}

func main() {
	mode := "watch"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	var err error
	switch mode {
	case "build":
		err = build()
	case "watch":
		err = watch()
	}
	if err != nil {
		log.Fatalln(err)
	}
}
