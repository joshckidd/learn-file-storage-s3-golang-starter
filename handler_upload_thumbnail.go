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
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
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
	defer file.Close()

	contentType := header.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to determine file type", err)
		return
	}
	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Bad file type", err)
		return
	}

	fileExtensions, err := mime.ExtensionsByType(mediaType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to determine file type", err)
		return
	}

	imageFileNameRaw := make([]byte, 32)
	rand.Read(imageFileNameRaw)
	imageFileName := base64.RawURLEncoding.EncodeToString(imageFileNameRaw)

	imageFile := fmt.Sprintf("%s%s", imageFileName, fileExtensions[0])
	imageFilePath := filepath.Join(cfg.assetsRoot, imageFile)

	newFile, err := os.Create(imageFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "File create error", err)
		return
	}

	_, err = io.Copy(newFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "File save error", err)
		return
	}

	videoMetadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", err)
		return
	}

	thumbURL := fmt.Sprintf("http://localhost:8091/%s", imageFilePath)

	newVideo := database.Video{
		ID:                videoID,
		ThumbnailURL:      &thumbURL,
		CreateVideoParams: videoMetadata.CreateVideoParams,
		VideoURL:          videoMetadata.VideoURL,
	}

	err = cfg.db.UpdateVideo(newVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Database error", err)
		return
	}
	respondWithJSON(w, http.StatusOK, newVideo)
}
