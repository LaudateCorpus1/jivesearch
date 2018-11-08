package frontend

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/jivesearch/jivesearch/log"
)

type proxyResponse struct {
	Brand
	Context `json:"-"`
	HTML    string `json:"-"`
}

func (f *Frontend) proxyHeaderHandler(w http.ResponseWriter, r *http.Request) *response {
	resp := &response{
		status:   http.StatusOK,
		template: "proxy_header",
		err:      nil,
	}

	resp.data = proxyResponse{
		Brand: f.Brand,
	}

	return resp
}

func (f *Frontend) proxyHandler(w http.ResponseWriter, r *http.Request) *response {
	resp := &response{
		status:   http.StatusOK,
		template: "proxy",
		data: proxyResponse{
			Brand: f.Brand,
		},
		err: nil,
	}

	u := r.FormValue("u")
	if u == "" {
		return resp
	}

	base, err := url.Parse(u)
	if err != nil {
		log.Info.Println(err)
	}

	res, err := get(base.String())
	if err != nil {
		log.Info.Println(err)
	}

	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Info.Println(err)
	}

	fmt.Println("Yo, we are removing all <li> tags....remember to remove this part...")
	doc.Find("li").Each(func(i int, s *goquery.Selection) {
		s.Remove()
	})

	// TODO: remove all comments...no need for them

	// remove all javascript
	doc.Find("script").Each(func(i int, s *goquery.Selection) {
		s.Remove()
	})

	// disable all forms
	doc.Find("form").Each(func(i int, s *goquery.Selection) {
		s.SetAttr("disabled", "disabled")
	})

	// proxy links
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		for _, href := range []string{"href"} {
			if lnk, ok := s.Attr(href); ok {
				u, err := url.Parse(lnk)
				if err != nil {
					log.Info.Println(err)
				}

				u, err = createProxyLink(base.ResolveReference(u))
				if err != nil {
					log.Info.Println(err)
				}

				s.SetAttr(href, u.String())
			}
		}
	})

	// proxy images
	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		for _, src := range []string{"src", "srcset"} {
			if lnk, ok := s.Attr(src); ok {
				if lnk == "" || isBase64(lnk) {
					continue
				}

				fields := strings.Fields(lnk)
				if src == "srcset" {
					lnk = fields[0]
				}

				u, err := url.Parse(lnk)
				if err != nil {
					log.Info.Println(err)
				}

				u = base.ResolveReference(u)
				key := hmacKey(u.String())
				l := fmt.Sprintf("/image/,s%v/%v", key, u.String())

				if len(fields) > 1 { // include the responsive size if srcset
					l = fmt.Sprintf("%v %v", l, strings.Join(fields, " "))
				}
				s.SetAttr(src, l)
			}
		}
	})

	// proxy url() within style tags
	doc.Find("style").Each(func(i int, s *goquery.Selection) {
		h := replaceCSS(base, s.Text())

		// replace the link with the css
		s.ReplaceWithHtml(fmt.Sprintf(`<style>%v</style>`, h))
	})

	// within each external css file, proxy all url() items
	doc.Find("link").Each(func(i int, s *goquery.Selection) {
		if rel, ok := s.Attr("rel"); ok && strings.ToLower(rel) == "stylesheet" {
			if lnk, ok := s.Attr("href"); ok {
				u, err := url.Parse(lnk)
				if err != nil {
					log.Info.Println(err)
				}

				u = base.ResolveReference(u)
				res, err := get(u.String())
				if err != nil {
					log.Info.Println(err)
				}

				defer res.Body.Close()

				h, err := ioutil.ReadAll(res.Body)
				if err != nil {
					log.Info.Println(err)
				}

				st := replaceCSS(base, string(h))

				// replace the link with the css
				s.ReplaceWithHtml(fmt.Sprintf(`<style>%v</style>`, st))
			}
		} else { // just proxy the href
			if lnk, ok := s.Attr("href"); ok {
				u, err := url.Parse(lnk)
				if err != nil {
					log.Info.Println(err)
				}

				u, err = createProxyLink(base.ResolveReference(u))
				if err != nil {
					log.Info.Println(err)
				}

				s.SetAttr("href", u.String())
			}
		}
	})

	h, err := doc.Html()
	//_, err = doc.Html()
	if err != nil {
		log.Info.Println(err)
	}
	//fmt.Println(h)

	resp.data = proxyResponse{
		Brand: f.Brand,
		HTML:  h,
	}

	return resp
}

func isBase64(s string) bool {
	return strings.HasPrefix(strings.ToLower(s), "data:")
}

// can have ', ", or no quotes
var reCSSLinkReplacer = regexp.MustCompile(`(url\(['"]?)(?P<link>.*?)['"]?\)`)

// https://stackoverflow.com/a/28005189/522962
func replaceAllSubmatchFunc(re *regexp.Regexp, b []byte, f func(s []byte) []byte) []byte {
	idxs := re.FindAllSubmatchIndex(b, -1)
	if len(idxs) == 0 {
		return b
	}
	l := len(idxs)
	ret := append([]byte{}, b[:idxs[0][0]]...)
	for i, pair := range idxs {
		ret = append(ret, f(b[pair[4]:pair[5]])...) // 2 & 3 are <url>. 4 & 5 are the <link>
		if i+1 < l {
			ret = append(ret, b[pair[1]:idxs[i+1][0]]...)
		}
	}
	ret = append(ret, b[idxs[len(idxs)-1][1]:]...)
	return ret
}

func replaceCSS(base *url.URL, s string) string {
	// replace any urls with a proxied link
	ss := replaceAllSubmatchFunc(reCSSLinkReplacer, []byte(s), func(ss []byte) []byte {
		if isBase64(string(ss)) { // base64 image
			return []byte(s)
		}

		m := string(ss)

		u, err := url.Parse(m)
		if err != nil {
			log.Info.Println(err)
		}

		u = base.ResolveReference(u)

		key := hmacKey(u.String())
		uu := fmt.Sprintf("/image/,s%v/%v", key, u.String())
		return []byte(fmt.Sprintf("url(%q)", uu))
	})

	return string(ss)
}

func createProxyLink(u *url.URL) (*url.URL, error) {
	uu, err := url.Parse("/proxy")
	if err != nil {
		return nil, err
	}

	q := uu.Query()
	q.Add("key", hmacKey(u.String()))
	q.Add("url", u.String())
	uu.RawQuery = q.Encode()
	return uu, err
}

func get(u string) (*http.Response, error) {
	// we don't want &httputil.ReverseProxy as we don't want to pass the user's IP address & other info.
	uri, err := url.Parse(u)
	if err != nil {
		return nil, err
	}

	client := http.Client{
		Timeout: 2 * time.Second,
	}
	request, err := http.NewRequest("GET", uri.String(), nil)
	if err != nil {
		return nil, err
	}

	return client.Do(request)
}
