package main

import (
	"net/http"
	"time"

	drivertypes "github.com/OpenListTeam/openlist-wasi-plugin-driver/binding/openlist/plugin-driver/types"
	"resty.dev/v3"

	"strconv"
)

/*
通过cookie获取数据
*/

// 获取文件和文件夹,获取到的文件大小、更改时间不可信
func (d *LanZou) GetAllFiles(folderID string) ([]drivertypes.Object, error) {
	folders, err := d.GetFolders(folderID)
	if err != nil {
		return nil, err
	}
	files, err := d.GetFiles(folderID)
	if err != nil {
		return nil, err
	}

	objs := make([]drivertypes.Object, 0, len(folders)+len(files))
	for _, folder := range folders {
		objs = append(objs, folder.ToObject())
	}
	for _, file := range files {
		objs = append(objs, file.ToObject())
	}
	return objs, nil
}

// 通过ID获取文件夹
func (d *LanZou) GetFolders(folderID string) ([]FileOrFolder, error) {
	var resp RespText[[]FileOrFolder]
	_, err := d.Doupload(func(req *resty.Request) {
		req.SetFormData(map[string]string{
			"task":      "47",
			"folder_id": folderID,
		})
	}, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Text, nil
}

// 通过ID获取文件
func (d *LanZou) GetFiles(folderID string) ([]FileOrFolder, error) {
	files := make([]FileOrFolder, 0)
	for pg := 1; ; pg++ {
		var resp RespText[[]FileOrFolder]
		_, err := d.Doupload(func(req *resty.Request) {
			req.SetFormData(map[string]string{
				"task":      "5",
				"folder_id": folderID,
				"pg":        strconv.Itoa(pg),
			})
		}, &resp)
		if err != nil {
			return nil, err
		}
		if len(resp.Text) == 0 {
			break
		}
		files = append(files, resp.Text...)
	}
	return files, nil
}

// 通过ID获取文件夹分享地址
func (d *LanZou) getFolderShareUrlByID(fileID string) (*FileShare, error) {
	var resp RespInfo[FileShare]
	_, err := d.Doupload(func(req *resty.Request) {
		req.SetFormData(map[string]string{
			"task":    "18",
			"file_id": fileID,
		})
	}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp.Info, nil
}

// 通过ID获取文件分享地址
func (d *LanZou) getFileShareUrlByID(fileID string) (*FileShare, error) {
	var resp RespInfo[FileShare]
	_, err := d.Doupload(func(req *resty.Request) {
		req.SetFormData(map[string]string{
			"task":    "22",
			"file_id": fileID,
		})
	}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp.Info, nil
}

// 通过下载头获取真实文件信息
func (d *LanZou) getFileRealInfo(downURL string) (int64, time.Time) {
	res, _ := d.Client.R().Head(downURL)
	if res == nil {
		return 0, time.Time{}
	}
	time, _ := http.ParseTime(res.Header().Get("Last-Modified"))
	size, _ := strconv.ParseInt(res.Header().Get("Content-Length"), 10, 64)
	return size, time
}
