package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

type VideoInfoData struct {
	Streams []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"streams"`
}

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxUploadSize = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	videoData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Internal Server Error", err)
		return
	}

	if videoData.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized user", nil)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	contentType := header.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Internal Server Error", err)
		return
	}
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid media type", err)
		return
	}

	extension := extractExtensionFromMediaType(mediaType)
	randomFileName, err := generateRandomFileName()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Internal Server Error", err)
		return
	}

	fileName := fmt.Sprintf("%s.%s", randomFileName, extension)
	tmpFile, err := os.CreateTemp("", fileName)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Internal Server Error", err)
		return
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = io.Copy(tmpFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Internal Server Error", err)
		return
	}

	_, err = tmpFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Internal Server Error", err)
		return
	}

	aspectRatio, err := getVideoAspectRatio(tmpFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Internal Server Error", err)
		return
	}

	directory := ""
	directory = setAspectRatioPrefixToKey(aspectRatio)
	fileName = path.Join(directory, fileName)

	fmt.Printf("KEY: %s", fileName)

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileName,
		Body:        tmpFile,
		ContentType: &mediaType,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Internal Server Error", err)
		return
	}

	url := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, fileName)
	videoData.VideoURL = &url

	err = cfg.db.UpdateVideo(videoData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Internal Server Error", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoData)
}

func getVideoAspectRatio(filepath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filepath)

	stdout := &bytes.Buffer{}
	cmd.Stdout = stdout

	err := cmd.Run()
	if err != nil {
		return "", err
	}

	var videoInfoData VideoInfoData
	err = json.Unmarshal(stdout.Bytes(), &videoInfoData)
	if err != nil {
		return "", nil
	}

	if len(videoInfoData.Streams) == 0 {
		return "", fmt.Errorf("no video streams found")
	}

	width := videoInfoData.Streams[0].Width
	height := videoInfoData.Streams[0].Height
	fmt.Println("WIDTH: ", width)
	fmt.Println("HEIGHT: ", height)

	if height == 0 {
		return "", fmt.Errorf("invalid height: cannot divide by zero")
	}

	if width == 16*height/9 {
		return "16:9", nil
	} else if height == 16*width/9 {
		return "9:16", nil
	}
	return "other", nil
}

func setAspectRatioPrefixToKey(aspectRatio string) string {
	prefix := ""

	switch aspectRatio {
	case "16:9":
		prefix = "landscape/"
	case "9:16":
		prefix = "portrait/"
	default:
		prefix = "other/"
	}

	return prefix
}
