package admin

import (
	"errors"
	"net/http"

	"github.com/janmarkuslanger/baseplate/internal/auth"
)

func (a *Admin) loginPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, http.StatusOK, a.pages["login"], view{Title: a.t("login.submit")})
}

func (a *Admin) login(w http.ResponseWriter, r *http.Request) {
	// Unauthenticated: bound the body before the form is parsed.
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	ses, err := a.Auth.Login(r.Context(), a.Reg.ClientIP(r), r.FormValue("email"), r.FormValue("password"))
	if err != nil {
		msg := a.tr(err)
		if !errors.Is(err, auth.ErrInvalidLogin) && !errors.Is(err, auth.ErrRateLimited) {
			a.Logger.Error("login", "err", err)
			msg = a.t("login.failed")
		}
		a.render(w, r, http.StatusUnauthorized, a.pages["login"], view{Title: a.t("login.submit"), Flash: msg, Page: r.FormValue("email")})
		return
	}
	a.Auth.SetCookie(w, r, ses)
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (a *Admin) logout(w http.ResponseWriter, r *http.Request) {
	a.Auth.Logout(w, r)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}
