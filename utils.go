package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/OpenListTeam/openlist-wasi-plugin-driver/adapter"
	"github.com/tidwall/gjson"
	"resty.dev/v3"
)

type ReqCallback func(client *resty.Request)

func (d *LanZou) Doupload(callback ReqCallback, resp interface{}) ([]byte, error) {
	return d.Post(MustUrlJoin(d.BaseUrl, "/doupload.php"), func(req *resty.Request) {
		req.SetQueryParams(map[string]string{
			"uid": d.uid,
			"vei": d.vei,
		})
		if callback != nil {
			callback(req)
		}
	}, resp)
}

func (d *LanZou) Post(url string, callback ReqCallback, resp interface{}) ([]byte, error) {
	return d.Request(http.MethodPost, url, callback, resp, false)
}

func (d *LanZou) Get(url string, callback ReqCallback) ([]byte, error) {
	return d.Request(http.MethodGet, url, callback, nil, false)
}

func (d *LanZou) Request(method string, url string, callback ReqCallback, resp interface{}, up bool) ([]byte, error) {
	data, err := d.request(method, url, callback, resp, up)
	// 只有在 cookie 过期时才需要特殊处理
	if !errors.Is(err, ErrCookieExpiration) || !d.IsAccount() {
		return data, err
	}

	// 使用 singleflight 来执行登录
	// 所有遇到 cookie 过期的 goroutine 都会调用 Do,
	// 但只有第一个会执行 d.Login() 函数，其他的会等待结果。
	_, err, _ = d.loginGroup.Do("login", func() (any, error) {
		_, loginErr := d.Login()
		if loginErr != nil {
			return 0, err
		}

		d.SaveConfig(&d.Addition)
		return 0, nil
	})
	// 检查登录过程是否出错
	if err != nil {
		return nil, err // 返回合并后的错误
	}

	// 登录成功后，重试原始的 post 请求
	return d.request(method, url, callback, resp, up)
}

func (d *LanZou) request(method string, url_ string, callback ReqCallback, resp any, up bool) ([]byte, error) {
	var client *resty.Client
	if up {
		client = d.UploadClient
	} else {
		client = d.Client
	}
	req := client.R()

	if callback != nil {
		callback(req)
	}

	result, err := req.Execute(method, url_)

	if err != nil {
		return nil, err
	}

	return checkError(result, resp)
}

// 检测可能的错误
func checkError(result *resty.Response, resp any) ([]byte, error) {
	data := result.Bytes()
	// UserAgent 被屏蔽的话返回的数据是空的
	if len(data) == 0 && result.StatusCode() == 200 {
		return nil, errors.New("page cannot be retrieved, please try using a new UserAgent")
	}

	zt := gjson.GetBytes(data, "zt")
	if zt.Raw == "" {
		return data, nil
	}
	// 处理json错误
	switch zt.Int() {
	case 1, 2, 4:
		if resp != nil {
			json.Unmarshal(data, resp)
		}
		return data, nil
	case 9: // 登录过期
		return data, errors.Join(ErrCookieExpiration, adapter.ErrUnauthorized)
	default:
		info := gjson.GetBytes(data, "inf").String()
		if info == "" {
			info = gjson.GetBytes(data, "info").String()
		}
		if info == "" {
			info = string(data)
		}
		return data, errors.New("error code: " + info)
	}
}

func (d *LanZou) Login() ([]*http.Cookie, error) {
	resp, err := d.ClientNotRedirect.R().SetFormData(map[string]string{
		"task":         "3",
		"uid":          d.Account,
		"pwd":          d.Password,
		"setSessionId": "",
		"setSig":       "",
		"setScene":     "",
		"setTocen":     "",
		"formhash":     "",
	}).Post(MustUrlJoin(d.BaseUrl, "/mlogin.php"))
	if err != nil {
		return nil, errors.Join(err, ErrCookieExpiration, adapter.ErrUnauthorized)
	}
	if gjson.GetBytes(resp.Bytes(), "zt").Int() != 1 {
		return nil, errors.Join(fmt.Errorf("login err: %s", resp.Bytes()), ErrCookieExpiration, adapter.ErrUnauthorized)
	}

	//  302 说明Cookie没有过期
	if resp.StatusCode() == 302 {
		return d.CookieJar.Cookies(resp.RawResponse.Request.URL), nil
	}

	return resp.Cookies(), nil
}

func (d *LanZou) getVeiAndUid() (vei string, uid string, err error) {
	var resp []byte
	resp, err = d.Get(MustUrlJoin(d.BaseUrl, "/mydisk.php"), func(client *resty.Request) {
		client.SetQueryParams(map[string]string{
			"item":   "files",
			"action": "index",
		})
	})
	if err != nil {
		return
	}

	// uid
	uids := regexp.MustCompile(`uid=([^'"&;]+)`).FindStringSubmatch(string(resp))
	if len(uids) < 2 {
		err = fmt.Errorf("uid variable not find")
		return
	}
	uid = uids[1]

	html := RemoveNotes(string(resp))
	// vei
	data, err := htmlJsonToMap(html)
	if err != nil {
		return
	}
	vei = data["vei"]
	return
}
