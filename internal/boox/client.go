package boox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"
)

type DeviceDetails struct {
	Host         string `json:"host"`
	ID           string `json:"id"`
	MAC          string `json:"mac"`
	Model        string `json:"model"`
	StorageTotal string `json:"storageTotal"`
	StorageUsed  string `json:"storageUsed"`
	DeviceType   string `json:"type"`
}

type LibraryResponse struct {
	BookCount          int           `json:"bookCount"`
	LibraryCount       int           `json:"libraryCount"`
	VisibleBookList    []LibraryBook `json:"visibleBookList"`
	VisibleLibraryList []Library     `json:"visibleLibraryList"`
}

type LibraryBook struct {
	Title string `json:"title"`
}

type Library struct {
	Title string `json:"title"`
}

type LibraryQueryParams struct {
	Limit           int
	Offset          int
	SortBy          string
	Order           string
	LibraryUniqueID string
}

type FolderCreationRequest struct {
	Parent interface{} `json:"parent"`
	Name   string      `json:"name"`
}

type FolderCreationResponse struct {
	ID string `json:"id"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	return &Client{baseURL: baseURL, httpClient: httpClient}
}

func (client *Client) CheckConnection(ctx context.Context) (*DeviceDetails, error) {
	endpoint := client.baseURL + "/api/device"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("unable to build device request: %w", err)
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("device request failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("device request failed: %s", string(body))
	}

	var device DeviceDetails
	if err := json.NewDecoder(response.Body).Decode(&device); err != nil {
		return nil, fmt.Errorf("unable to decode device response: %w", err)
	}

	return &device, nil
}

func (client *Client) GetLibraryTitles(ctx context.Context, params LibraryQueryParams) ([]string, error) {
	queryURL, err := client.constructLibraryURL(params)
	if err != nil {
		return nil, fmt.Errorf("error constructing URL: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, queryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error building request: %w", err)
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("error making HTTP request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("unexpected status code: %d %s", response.StatusCode, string(body))
	}

	var libraryResp LibraryResponse
	if err := json.NewDecoder(response.Body).Decode(&libraryResp); err != nil {
		return nil, fmt.Errorf("error unmarshaling JSON: %w", err)
	}

	titles := make([]string, 0, len(libraryResp.VisibleBookList)+len(libraryResp.VisibleLibraryList))
	for _, book := range libraryResp.VisibleBookList {
		titles = append(titles, book.Title)
	}

	for _, library := range libraryResp.VisibleLibraryList {
		titles = append(titles, library.Title)
	}

	return titles, nil
}

func (client *Client) CreateFolder(ctx context.Context, parentID *string, title string) (string, error) {
	endpoint := client.baseURL + "/api/library"
	payload := FolderCreationRequest{
		Parent: nil,
		Name:   title,
	}
	if parentID != nil {
		payload.Parent = *parentID
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("error marshaling JSON: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("error creating request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("error making POST request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		return "", fmt.Errorf("unexpected status code: %d %s", response.StatusCode, string(body))
	}

	var folderResponse FolderCreationResponse
	if err := json.NewDecoder(response.Body).Decode(&folderResponse); err != nil {
		return "", fmt.Errorf("error decoding response: %w", err)
	}

	return folderResponse.ID, nil
}

func (client *Client) UploadFile(ctx context.Context, parentID, fileName string, fileData []byte) error {
	endpoint := client.baseURL + "/api/library/upload"

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return fmt.Errorf("unable to create form file: %w", err)
	}

	if _, err := part.Write(fileData); err != nil {
		return fmt.Errorf("unable to write file data: %w", err)
	}

	if parentID != "" {
		writer.WriteField("parent", parentID)
	}
	writer.WriteField("name", fileName)

	if err := writer.Close(); err != nil {
		return fmt.Errorf("unable to finalize form: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return fmt.Errorf("unable to create upload request: %w", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())

	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("upload request failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf("upload failed: %d %s", response.StatusCode, string(body))
	}

	return nil
}

func (client *Client) RenameItem(ctx context.Context, idString, newName string) error {
	endpoint := client.baseURL + "/api/library/rename"

	payload := struct {
		IDString string `json:"idString"`
		File     string `json:"file"`
		Name     string `json:"name"`
	}{
		IDString: idString,
		File:     idString,
		Name:     newName,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("error marshaling JSON: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf("unexpected status code: %d, body: %s", response.StatusCode, string(body))
	}

	return nil
}

func (client *Client) constructLibraryURL(params LibraryQueryParams) (string, error) {
	libraryURL := client.baseURL + "/api/library"

	u, err := url.Parse(libraryURL)
	if err != nil {
		return "", err
	}

	q := u.Query()
	args := map[string]interface{}{
		"limit":  params.Limit,
		"offset": params.Offset,
		"sortBy": params.SortBy,
		"order":  params.Order,
	}
	if params.LibraryUniqueID != "" {
		args["libraryUniqueId"] = params.LibraryUniqueID
	}

	argsJSON, err := json.Marshal(args)
	if err != nil {
		return "", err
	}

	q.Set("args", string(argsJSON))
	u.RawQuery = q.Encode()

	return u.String(), nil
}
