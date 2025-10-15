package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"resty.dev/v3"
	"resty.dev/v3/cookiejar"

	"golang.org/x/sync/singleflight"

	_ "github.com/OpenListTeam/go-wasi-http/wasihttp"

	openlistwasiplugindriver "github.com/OpenListTeam/openlist-wasi-plugin-driver"
	"github.com/OpenListTeam/openlist-wasi-plugin-driver/adapter"
	drivertypes "github.com/OpenListTeam/openlist-wasi-plugin-driver/binding/openlist/plugin-driver/types"
	httptypes "github.com/OpenListTeam/openlist-wasi-plugin-driver/binding/wasi/http/types"

	"go.bytecodealliance.org/cm"
)

func main() {

}

var DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0"

var _ openlistwasiplugindriver.Driver = (*LanZou)(nil)
var _ openlistwasiplugindriver.Mkdir = (*LanZou)(nil)
var _ openlistwasiplugindriver.Move = (*LanZou)(nil)
var _ openlistwasiplugindriver.Rename = (*LanZou)(nil)
var _ openlistwasiplugindriver.Remove = (*LanZou)(nil)
var _ openlistwasiplugindriver.Put = (*LanZou)(nil)

type LanZou struct {
	openlistwasiplugindriver.DriverHandle
	Addition

	CookieJar         *cookiejar.Jar
	Client            *resty.Client
	ClientNotRedirect *resty.Client
	UploadClient      *resty.Client

	uid string
	vei string

	loginGroup singleflight.Group
}

func (*LanZou) GetProperties() drivertypes.DriverProps {
	return drivertypes.DriverProps{
		Name: "LanZou",
	}
}

func (*LanZou) GetFormMeta() []drivertypes.FormField {
	return []drivertypes.FormField{
		{
			Name:  "type",
			Label: "Type",
			Kind:  drivertypes.FieldKindSelectKind(cm.ToList([]string{"account", "cookie", "url"})),
		},
		{
			Name:  "account",
			Label: "Account",
			Kind:  drivertypes.FieldKindStringKind(""),
		},
		{
			Name:  "password",
			Label: "Password",
			Kind:  drivertypes.FieldKindPasswordKind(""),
		},
		{
			Name:  "cookie",
			Label: "Cookie",
			Kind:  drivertypes.FieldKindPasswordKind(""),
			Help:  "about 15 days valid, ignore if shareUrl is used",
		},
		{
			Name:  "root_folder_id",
			Label: "RootFolderId",
			Kind:  drivertypes.FieldKindStringKind(""),
		},

		{
			Name:  "share_password",
			Label: "Share Password",
			Kind:  drivertypes.FieldKindStringKind(""),
		},
		{
			Name:     "base_url",
			Label:    "Base URL",
			Kind:     drivertypes.FieldKindStringKind("https://pc.woozooo.com"),
			Required: true,
			Help:     "basic URL for file operation",
		},
		{
			Name:     "share_url",
			Label:    "Share URL",
			Kind:     drivertypes.FieldKindStringKind("https://wwop.lanzoul.com"),
			Required: true,
			Help:     "used to get the sharing page",
		},
		{
			Name:     "user_agent",
			Label:    "User Agent",
			Kind:     drivertypes.FieldKindStringKind(DefaultUserAgent),
			Required: true,
		},
		{
			Name:  "repair_file_info",
			Label: "Repair File Info",
			Kind:  drivertypes.FieldKindBooleanKind(true),
			Help:  "To use webdav, you need to enable it",
		},
	}
}

func (d *LanZou) Init(ctx context.Context) error {
	if err := d.LoadConfig(&d.Addition); err != nil {
		return err
	}

	if d.UserAgent == "" {
		d.UserAgent = DefaultUserAgent
	}

	cookieJar, _ := cookiejar.New(nil)
	createClient := func() *resty.Client {
		return resty.New().SetHeaders(map[string]string{
			"Referer":    d.BaseUrl,
			"User-Agent": d.UserAgent,
		}).SetCookieJar(cookieJar)
	}
	createClient2 := func() *resty.Client {
		client := createClient()

		// 1. Add Conditions: Decide IF a retry is needed.
		client.AddRetryConditions(
			func(resp *resty.Response, err error) bool {
				// If there's a transport error, don't retry unless it's a known temporary issue
				if err != nil {
					// You can add more specific error checks here if needed
					return false
				}

				// Condition 1: Check for the anti-bot challenge
				if strings.Contains(resp.String(), "acw_sc__v2") {
					return true // Yes, we need to retry
				}

				// Condition 2: Check for "operation busy"
				if gjson.GetBytes(resp.Bytes(), "zt").Int() == 4 {
					return true // Yes, we need to retry
				}

				// For any other case, do not retry.
				return false
			},
		)
		// 2. Add Hooks: Perform actions BEFORE the next retry.
		client.AddRetryHooks(
			func(resp *resty.Response, err error) {
				// This hook runs only if one of the conditions above returned true.

				// If the response contains the anti-bot challenge...
				if strings.Contains(resp.String(), "acw_sc__v2") {
					// Calculate the required cookie value
					vs, calcErr := CalcAcwScV2(resp.String())
					if calcErr != nil {
						// Log the error. The retry will still happen, but might fail again.
						openlistwasiplugindriver.Warnf("lanzou: err => acw_sc__v2 validation error, data => %s\n", resp.String())
						return
					}

					// Set the cookie for the NEXT request
					u, _ := url.Parse(resp.Request.URL)
					cookieJar.SetCookies(u, []*http.Cookie{{Name: "acw_sc__v2", Value: vs}})
				}
			},
		)

		return client.SetRetryCount(3).
			SetRetryWaitTime(200 * time.Millisecond).
			SetRetryMaxWaitTime(1 * time.Second)
	}

	d.CookieJar = cookieJar
	d.Client = createClient2()
	d.ClientNotRedirect = createClient2().SetRedirectPolicy(resty.NoRedirectPolicy())
	d.UploadClient = createClient().SetRetryCount(0).SetTimeout(time.Minute * 2)

	switch d.Type {
	case "account":
		_, err := d.Login()
		if err != nil {
			return err
		}
	case "cookie":
		cookies, err := cookiejar.ParseCookie(d.Cookie)
		if err != nil {
			return err
		}
		if err := SetCookieToJar(cookieJar, d.BaseUrl, cookies); err != nil {
			return err
		}
	default:
		return nil
	}

	if d.RootFolderID == "" {
		d.RootFolderID = "-1"
	}

	vei, uid, err := d.getVeiAndUid()
	if err != nil {
		return err
	}
	d.vei = vei
	d.uid = uid
	return nil
}

func (d *LanZou) Drop(ctx context.Context) error {
	d.uid = ""
	d.vei = ""

	d.CookieJar = nil
	d.Client = nil
	d.ClientNotRedirect = nil
	d.UploadClient = nil
	return nil
}

func (d *LanZou) ListFiles(ctx context.Context, dir drivertypes.Object) ([]drivertypes.Object, error) {
	var objs []drivertypes.Object
	var err error
	if d.IsCookie() || d.IsAccount() {
		objs, err = d.GetAllFiles(dir.ID)
	} else {
		objs, err = d.GetFileOrFolderByShareUrl(dir.ID, d.SharePassword)
	}

	if err != nil {
		return nil, err
	}
	return objs, nil
}

func (d *LanZou) LinkFile(ctx context.Context, file drivertypes.Object, args drivertypes.LinkArgs) (*drivertypes.LinkResource, *drivertypes.Object, error) {
	var (
		err   error
		dfile *FileOrFolderByShareUrl
	)
	extra := adapter.ExtraToMap(file.Extra)
	patch := false

	switch adapter.ExtraGetDefable(file.Extra, "type") {
	case "0":
		if extra["fid"] == "" {
			sfile, err := d.getFileShareUrlByID(file.ID)
			if err != nil {
				return nil, nil, err
			}
			extra["fid"] = sfile.FID
			extra["pwd"] = sfile.Pwd
			patch = true
		}
	case "1":
	default:
		return nil, nil, errors.New("file Information Lost")
	}

	dfile, err = d.GetFilesByShareUrl(extra["fid"], extra["pwd"])
	if err != nil {
		return nil, nil, err
	}

	if d.RepairFileInfo {
		if _, ok := extra["repair"]; !ok {
			size, time := d.getFileRealInfo(dfile.Url)
			file.Size = size
			file.Created = drivertypes.Duration(time.UnixNano())
			file.Modified = drivertypes.Duration(time.UnixNano())
			extra["repair"] = ""
			patch = true
		}
	}

	if patch {
		file.Extra = adapter.ExtraFormMap(extra)
	}

	exp := GetExpirationTime(dfile.Url)
	header := httptypes.NewFields()
	header.Append("User-Agent", httptypes.FieldValue(cm.ToList([]byte(d.UserAgent))))
	link := drivertypes.LinkResourceDirect(drivertypes.LinkInfo{
		URL:        dfile.Url,
		Headers:    header,
		Expiration: cm.Some(drivertypes.Duration(exp)),
	})
	return &link, &file, nil
}

func (d *LanZou) MakeDir(ctx context.Context, parentDir drivertypes.Object, dirName string) (*drivertypes.Object, error) {
	if d.IsCookie() || d.IsAccount() {
		data, err := d.Doupload(func(req *resty.Request) {
			req.SetContext(ctx)
			req.SetFormData(map[string]string{
				"task":               "2",
				"parent_id":          parentDir.ID,
				"folder_name":        dirName,
				"folder_description": "",
			})
		}, nil)
		if err != nil {
			return nil, err
		}

		folder := FileOrFolder{
			Name:  dirName,
			FolID: gjson.GetBytes(data, "text").String(),
		}
		obj := folder.ToObject()
		return &obj, nil
	}
	return nil, adapter.ErrNotSupport
}

func (d *LanZou) Move(ctx context.Context, srcObj, dstDir drivertypes.Object) (*drivertypes.Object, error) {
	if d.IsCookie() || d.IsAccount() {
		if !srcObj.IsFolder {
			_, err := d.Doupload(func(req *resty.Request) {
				req.SetContext(ctx)
				req.SetFormData(map[string]string{
					"task":      "20",
					"folder_id": dstDir.ID,
					"file_id":   srcObj.ID,
				})
			}, nil)
			if err != nil {
				return nil, err
			}
			return &srcObj, nil
		}
	}
	return nil, adapter.ErrNotSupport
}

func (d *LanZou) Rename(ctx context.Context, srcObj drivertypes.Object, newName string) (*drivertypes.Object, error) {
	if d.IsCookie() || d.IsAccount() {
		if !srcObj.IsFolder {
			_, err := d.Doupload(func(req *resty.Request) {
				req.SetContext(ctx)
				req.SetFormData(map[string]string{
					"task":      "46",
					"file_id":   srcObj.ID,
					"file_name": newName,
					"type":      "2",
				})
			}, nil)
			if err != nil {
				return nil, err
			}
			srcObj.Name = newName
			return &srcObj, nil
		}
	}
	return nil, adapter.ErrNotSupport
}

func (d *LanZou) Remove(ctx context.Context, obj drivertypes.Object) error {
	if d.IsCookie() || d.IsAccount() {
		_, err := d.Doupload(func(req *resty.Request) {
			req.SetContext(ctx)
			if obj.IsFolder {
				req.SetFormData(map[string]string{
					"task":      "3",
					"folder_id": obj.ID,
				})
			} else {
				req.SetFormData(map[string]string{
					"task":    "6",
					"file_id": obj.ID,
				})
			}
		}, nil)
		return err
	}
	return adapter.ErrNotSupport
}

func (d *LanZou) Put(ctx context.Context, dstDir drivertypes.Object, file adapter.UploadRequest) (*drivertypes.Object, error) {
	if d.IsCookie() || d.IsAccount() {
		stream, err := file.Streams()
		if err != nil {
			return nil, err
		}
		defer stream.Close()

		var resp RespText[[]FileOrFolder]
		_, err = d.Request(http.MethodPost, MustUrlJoin(d.BaseUrl, "/html5up.php"), func(client *resty.Request) {
			client.SetContext(ctx).
				SetMultipartFormData(map[string]string{
					"task":           "1",
					"vie":            "2",
					"ve":             "2",
					"id":             "WU_FILE_0",
					"name":           file.Object.Name,
					"folder_id_bb_n": dstDir.ID,
				}).
				SetFileReader("upload_file", file.Object.Name, stream)
		}, &resp, true)

		if err != nil {
			return nil, err
		}
		obj := resp.Text[0].ToObject()
		return &obj, nil
	}
	return nil, adapter.ErrNotSupport
}
