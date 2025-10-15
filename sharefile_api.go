package main

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	openlistwasiplugindriver "github.com/OpenListTeam/openlist-wasi-plugin-driver"
	drivertypes "github.com/OpenListTeam/openlist-wasi-plugin-driver/binding/openlist/plugin-driver/types"
	"github.com/tidwall/gjson"
	"resty.dev/v3"
)

/*
通过分享链接获取数据
*/

// 判断类容
var isFileReg = regexp.MustCompile(`class="fileinfo"|id="file"|文件描述`)
var isFolderReg = regexp.MustCompile(`id="infos"`)

// 获取文件文件夹基础信息

// 获取文件名称
var nameFindReg = regexp.MustCompile(`<title>(.+?) - 蓝奏云</title>|id="filenajax">(.+?)</div>|var filename = '(.+?)';|<div style="font-size.+?>([^<>].+?)</div>|<div class="filethetext".+?>([^<>]+?)</div>`)

// 获取文件大小
var sizeFindReg = regexp.MustCompile(`(?i)大小\W*([0-9.]+\s*[bkm]+)`)

// 获取文件时间
var timeFindReg = regexp.MustCompile(`\d+\s*[秒天分小][钟时]?前|[昨前]天|\d{4}-\d{2}-\d{2}`)

// 查找分享文件夹子文件夹ID和名称
var findSubFolderReg = regexp.MustCompile(`(?i)(?:folderlink|mbxfolder).+href="/(.+?)"(?:.+filename")?>(.+?)<`)

// 获取下载页面链接
var findDownPageParamReg = regexp.MustCompile(`<iframe.*?src="(.+?)"`)

// 获取文件ID
var findFileIDReg = regexp.MustCompile(`'/ajaxm\.php\?file=(\d+)'`)

// 获取分享链接主界面
func (d *LanZou) getShareUrlHtml(shareID string) (string, error) {
	firstPageData, err := d.Get(MustUrlJoin(d.ShareUrl, shareID), nil)
	if err != nil {
		return "", err
	}

	firstPageDataStr := RemoveNotes(string(firstPageData))
	if strings.Contains(firstPageDataStr, "取消分享") {
		return "", ErrFileShareCancel
	}
	if strings.Contains(firstPageDataStr, "文件不存在") {
		return "", ErrFileNotExist
	}

	return firstPageDataStr, nil
}

// 通过分享链接获取文件或文件夹
func (d *LanZou) GetFileOrFolderByShareUrl(shareID, pwd string) ([]drivertypes.Object, error) {
	pageData, err := d.getShareUrlHtml(shareID)
	if err != nil {
		return nil, err
	}
	if !isFileReg.MatchString(pageData) {
		files, err := d.getFolderByShareUrl(pwd, pageData)
		if err != nil {
			return nil, err
		}
		return MustSliceConvert(files, func(file FileOrFolderByShareUrl) drivertypes.Object {
			return file.ToObject()
		}), nil
	} else {
		file, err := d.getFilesByShareUrl(shareID, pwd, pageData)
		if err != nil {
			return nil, err
		}
		return []drivertypes.Object{file.ToObject()}, nil
	}
}

// 通过分享链接获取文件(下载链接也使用此方法)
// FileOrFolderByShareUrl 包含 pwd 和 url 字段
// 参考 https://github.com/zaxtyson/LanZouCloud-API/blob/ab2e9ec715d1919bf432210fc16b91c6775fbb99/lanzou/api/core.py#L440
func (d *LanZou) GetFilesByShareUrl(shareID, pwd string) (file *FileOrFolderByShareUrl, err error) {
	pageData, err := d.getShareUrlHtml(shareID)
	if err != nil {
		return nil, err
	}
	return d.getFilesByShareUrl(shareID, pwd, pageData)
}
func (d *LanZou) getFolderByShareUrl(pwd string, sharePageData string) ([]FileOrFolderByShareUrl, error) {
	from, err := htmlJsonToMap(sharePageData)
	if err != nil {
		return nil, err
	}
	if len(from) == 0 {
		return nil, errors.New("htmlJsonToMap not find data")
	}

	files := make([]FileOrFolderByShareUrl, 0)
	// vip获取文件夹
	floders := findSubFolderReg.FindAllStringSubmatch(sharePageData, -1)
	for _, floder := range floders {
		if len(floder) == 3 {
			files = append(files, FileOrFolderByShareUrl{
				// Pwd: pwd, // 子文件夹不加密
				ID:       floder[1],
				NameAll:  floder[2],
				IsFloder: true,
			})
		}
	}

	// 获取文件
	from["pwd"] = pwd
	for page := 1; ; page++ {
		from["pg"] = strconv.Itoa(page)
		var resp FileOrFolderByShareUrlResp
		_, err := d.Post(MustUrlJoin(d.ShareUrl, "/filemoreajax.php"), func(req *resty.Request) { req.SetFormData(from) }, &resp)
		if err != nil {
			return nil, err
		}
		// 文件夹中的文件加密
		for i := 0; i < len(resp.Text); i++ {
			resp.Text[i].Pwd = pwd
		}
		if len(resp.Text) == 0 {
			break
		}
		files = append(files, resp.Text...)
		time.Sleep(time.Second)
	}
	return files, nil
}

func (d *LanZou) getFilesByShareUrl(shareID, pwd string, sharePageData string) (*FileOrFolderByShareUrl, error) {
	var (
		param       map[string]string
		downloadUrl string
		file        FileOrFolderByShareUrl
	)

	// 删除注释
	sharePageData = RemoveNotes(sharePageData)
	sharePageData = RemoveJSComment(sharePageData)

	// 需要密码
	if strings.Contains(sharePageData, "pwdload") || strings.Contains(sharePageData, "passwddiv") {
		sharePageData, err := getJSFunctionByName(sharePageData, "down_p")
		if err != nil {
			return nil, err
		}
		param, err = htmlJsonToMap(sharePageData)
		if err != nil {
			return nil, err
		}
		param["p"] = pwd

		fileIDs := findFileIDReg.FindStringSubmatch(sharePageData)
		var fileID string
		if len(fileIDs) > 1 {
			fileID = fileIDs[1]
		} else {
			return nil, errors.New("not find file id")
		}

		var resp FileShareInfoAndUrlResp[string]
		_, err = d.Post(MustUrlJoin(d.ShareUrl, "/ajaxm.php"), func(req *resty.Request) {
			req.SetFormData(param).SetQueryParam("file", fileID)
		}, &resp)
		if err != nil {
			return nil, err
		}

		file.NameAll = resp.Inf
		file.Pwd = pwd
		downloadUrl = resp.GetDownloadUrl()
	} else {
		urlpaths := findDownPageParamReg.FindStringSubmatch(sharePageData)
		if len(urlpaths) != 2 {
			openlistwasiplugindriver.Errorf("lanzou: err => not find file page param ,data => %s\n", sharePageData)
			return nil, errors.New("not find file page param")
		}

		data, err := d.Get(joinURL(d.ShareUrl, urlpaths[1]), nil)
		if err != nil {
			return nil, err
		}

		nextPageData := RemoveNotes(string(data))
		param, err = htmlJsonToMap(nextPageData)
		if err != nil {
			return nil, err
		}

		fileIDs := findFileIDReg.FindStringSubmatch(nextPageData)
		var fileID string
		if len(fileIDs) > 1 {
			fileID = fileIDs[1]
		} else {
			return nil, errors.New("not find file id")
		}

		var resp FileShareInfoAndUrlResp[int]
		_, err = d.Post(MustUrlJoin(d.ShareUrl, "/ajaxm.php"), func(req *resty.Request) {
			req.SetFormData(param).SetQueryParam("file", fileID)
		}, &resp)
		if err != nil {
			return nil, err
		}
		downloadUrl = resp.GetDownloadUrl()

		names := nameFindReg.FindStringSubmatch(sharePageData)
		if len(names) > 1 {
			for _, name := range names[1:] {
				if name != "" {
					file.NameAll = name
					break
				}
			}
		}
	}

	if downloadUrl == "" {
		return nil, errors.New("download url is null")
	}

	sizes := sizeFindReg.FindStringSubmatch(sharePageData)
	if len(sizes) == 2 {
		file.Size = sizes[1]
	}
	file.ID = shareID
	file.Time = timeFindReg.FindString(sharePageData)

	// 重定向获取真实链接
	resp, err := d.ClientNotRedirect.R().
		SetHeaders(map[string]string{
			"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6",
		}).
		SetCookie(&http.Cookie{
			Name:  "down_ip",
			Value: "1",
		}).Get(downloadUrl)
	if err != nil {
		return nil, err
	}

	switch resp.StatusCode() {
	case 301, 302:
		file.Url = resp.Header().Get("Location")
	case 200:
		param, err = htmlJsonToMap(resp.String())
		if err != nil {
			return nil, err
		}
		param["el"] = "2"

		data, err := d.Post(MustUrlJoin(d.ShareUrl, "/ajax.php"), func(req *resty.Request) {
			req.SetFormData(param).SetCookie(&http.Cookie{
				Name:  "down_ip",
				Value: "1",
			})
		}, nil)
		if err != nil {
			return nil, err
		}
		file.Url = gjson.GetBytes(data, "url").String()
	default:
		s := resp.String()
		maxLen := 64
		if len(s) > maxLen {
			s = s[:maxLen] + "..."
		}
		return nil, fmt.Errorf("get download err: code %d content %d(%s)", resp.StatusCode(), len(resp.Bytes()), s)
	}
	return &file, nil
}
