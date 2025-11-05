package main

import (
	openlistwasiplugindriver "github.com/OpenListTeam/openlist-wasi-plugin-driver"
)

type Addition struct {
	Type string `json:"type"`

	Account  string `json:"account"`
	Password string `json:"password"`

	Cookie string `json:"cookie"`

	openlistwasiplugindriver.RootID

	SharePassword  string `json:"share_password"`
	BaseUrl        string `json:"base_url"`
	ShareUrl       string `json:"share_url"`
	UserAgent      string `json:"user_agent"`
	RepairFileInfo bool   `json:"repair_file_info"`
}

func (a *Addition) IsCookie() bool {
	return a.Type == "cookie"
}

func (a *Addition) IsAccount() bool {
	return a.Type == "account"
}

func init() {
	openlistwasiplugindriver.RegisterDriver(&LanZou{})
}
