package admin

import (
	"errors"
	"net/http"

	"github.com/janmarkuslanger/baseplate/internal/auth"
)

func (a *Admin) loginPage(w http.ResponseWriter, r *http.Request) {
	a.renderLogin(w, r, http.StatusOK, "", "")
}

func (a *Admin) renderLogin(w http.ResponseWriter, r *http.Request, status int, flash, email string) {
	a.render(w, r, status, a.pages["login"], view{Title: a.t("login.submit"), CSRF: a.Auth.LoginCSRF(w, r), Flash: flash, Page: email})
}

func (a *Admin) login(w http.ResponseWriter, r *http.Request) {
	// Unauthenticated: bound the body before the form is parsed.
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if !a.Auth.CheckLoginCSRF(r) {
		a.renderLogin(w, r, http.StatusForbidden, a.t("auth.csrf"), r.FormValue("email"))
		return
	}
	ses, err := a.Auth.Login(r.Context(), a.Reg.ClientIP(r), r.FormValue("email"), r.FormValue("password"))
	if err != nil {
		msg := a.tr(err)
		if !errors.Is(err, auth.ErrInvalidLogin) && !errors.Is(err, auth.ErrRateLimited) {
			a.Logger.Error("login", "err", err)
			msg = a.t("login.failed")
		}
		a.renderLogin(w, r, http.StatusUnauthorized, msg, r.FormValue("email"))
		return
	}
	a.Auth.SetCookie(w, r, ses)
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (a *Admin) logout(w http.ResponseWriter, r *http.Request) {
	a.Auth.Logout(w, r)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}
