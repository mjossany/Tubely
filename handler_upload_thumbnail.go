package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Error parsin media type", err)
		return
	}

	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Content-Type must be image/jpeg or image/png", err)
		return
	}

	var fileExt string
	switch mediaType {
	case "image/jpeg":
		fileExt = "jpg"
	case "image/png":
		fileExt = "png"
	case "image/gif":
		fileExt = "gif"
	default:
		respondWithError(w, http.StatusBadRequest, "Unsupported content type", nil)
		return
	}

	videoMetadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error while retrieving video metadata from database", err)
		return
	}
	if userID != videoMetadata.CreateVideoParams.UserID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized user", nil)
		return
	}

	randomBytes := make([]byte, 32)

	_, err = rand.Read(randomBytes)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Internal Server Error", err)
		return
	}

	randomFileName := base64.RawURLEncoding.EncodeToString(randomBytes)

	filename := fmt.Sprintf("%s.%s", randomFileName, fileExt)
	thumbnailFilePath := filepath.Join(cfg.assetsRoot, filename)
	systemFile, err := os.Create(thumbnailFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating thumbnail file", err)
		return
	}
	defer systemFile.Close()
	defer func() {
		if err != nil {
			os.Remove(thumbnailFilePath)
		}
	}()

	_, err = io.Copy(systemFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error copying file content", err)
		return
	}

	thumbnailURL := fmt.Sprintf("http://localhost:%s/assets/%s.%s", cfg.port, randomFileName, fileExt)

	videoMetadata.ThumbnailURL = &thumbnailURL

	err = cfg.db.UpdateVideo(videoMetadata)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error while updating video thumbnail url", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoMetadata)
}
