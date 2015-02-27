package cas

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"

	"github.com/golang/glog"
)

type Client struct {
	url    *url.URL
	store  TicketStore
	client *http.Client
}

func NewClient(options *Options) *Client {
	if glog.V(2) {
		glog.Infof("cas: new client with options %v", options)
	}

	var store TicketStore
	if options.Store != nil {
		store = options.Store
	} else {
		store = &MemoryStore{}
	}

	return &Client{
		url:    options.URL,
		store:  store,
		client: &http.Client{},
	}
}

func (c *Client) Handle(h http.Handler) http.Handler {
	return &clientHandler{
		c:    c,
		h:    h,
		seen: make(map[string]string),
	}
}

func (c *Client) HandleFunc(h func(http.ResponseWriter, *http.Request)) http.Handler {
	return c.Handle(http.HandlerFunc(h))
}

func (c *Client) LoginUrlForRequest(r *http.Request) (string, error) {
	u, err := c.url.Parse("login")
	if err != nil {
		return "", err
	}

	q := u.Query()
	q.Add("service", sanitisedURLString(r.URL))
	u.RawQuery = q.Encode()

	return u.String(), nil
}

func (c *Client) ServiceValidateUrlForService(ticket string, service *url.URL) (string, error) {
	u, err := c.url.Parse("serviceValidate")
	if err != nil {
		return "", err
	}

	q := u.Query()
	q.Add("service", sanitisedURLString(service))
	q.Add("ticket", ticket)
	u.RawQuery = q.Encode()

	return u.String(), nil
}

func (c *Client) ValidateUrlForService(ticket string, service *url.URL) (string, error) {
	u, err := c.url.Parse("validate")
	if err != nil {
		return "", err
	}

	q := u.Query()
	q.Add("service", sanitisedURLString(service))
	q.Add("ticket", ticket)
	u.RawQuery = q.Encode()

	return u.String(), nil
}

func (c *Client) RedirectToCas(w http.ResponseWriter, r *http.Request) {
	u, err := c.LoginUrlForRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	if glog.V(2) {
		glog.Infof("Redirecting client to %v with status %v", u, http.StatusFound)
	}

	http.Redirect(w, r, u, http.StatusFound)
}

func (c *Client) validateTicket(ticket string, service *url.URL) error {
	if glog.V(2) {
		glog.Infof("Validating ticket %v for service %v", ticket, service)
	}

	u, err := c.ServiceValidateUrlForService(ticket, service)
	if err != nil {
		return err
	}

	r, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}

	r.Header.Add("User-Agent", "Golang CAS client gopkg.in/cas.v1")

	if glog.V(2) {
		glog.Infof("Attempting ticket validation with %v", r.URL)
	}

	resp, err := c.client.Do(r)
	if err != nil {
		return err
	}

	if glog.V(2) {
		glog.Infof("Request %v %v returned %v",
			r.Method, r.URL,
			resp.Status)
	}

	if resp.StatusCode == http.StatusNotFound {
		return c.validateTicketCas1(ticket, service)
	}

	body, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()

	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cas: validate ticket: %v", string(body))
	}

	if glog.V(2) {
		glog.Infof("Received authentication response\n%v", string(body))
	}

	success, err := ParseServiceResponse(body)
	if err != nil {
		return err
	}

	if glog.V(2) {
		glog.Infof("Parsed ServiceResponse: %#v", success)
	}

	if err := c.store.Write(ticket, success); err != nil {
		return err
	}

	return nil
}

func (c *Client) validateTicketCas1(ticket string, service *url.URL) error {
	u, err := c.ValidateUrlForService(ticket, service)
	if err != nil {
		return err
	}

	r, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}

	r.Header.Add("User-Agent", "Golang CAS client gopkg.in/cas.v1")

	resp, err := c.client.Do(r)
	if err != nil {
		return err
	}

	data, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()

	if err != nil {
		return err
	}

	body := string(data)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cas: validate ticket: %v", body)
	}

	if body == "no\n\n" {
		return nil // not logged in
	}

	success := &AuthenticationResponse{
		User: body[4 : len(body)-1],
	}

	if err := c.store.Write(ticket, success); err != nil {
		return err
	}

	return nil
}
