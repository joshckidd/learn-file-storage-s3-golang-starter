package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
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

	fmt.Println("uploading video", videoID, "by user", userID)

	const maxMemory = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)

	file, header, err := r.FormFile("video")
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
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Bad file type", err)
		return
	}

	newFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "File create error", err)
		return
	}

	defer os.Remove(newFile.Name())
	defer newFile.Close()

	_, err = io.Copy(newFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "File save error", err)
		return
	}

	prefix, err := getVideoAspectRatio(newFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "File information error", err)
		return
	}

	fastFileName, err := processVideoForFastStart(newFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "File convert error", err)
		return
	}

	fastFile, err := os.Open(fastFileName)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "File convert read error", err)
		return
	}
	defer os.Remove(fastFileName)
	defer fastFile.Close()

	videoMetadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", err)
		return
	}

	imageFileNameRaw := make([]byte, 32)
	rand.Read(imageFileNameRaw)
	imageFileName := base64.RawURLEncoding.EncodeToString(imageFileNameRaw)
	imageFile := fmt.Sprintf("%s/%s.mp4", prefix, imageFileName)

	params := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &imageFile,
		Body:        fastFile,
		ContentType: &mediaType,
	}
	cfg.s3Client.PutObject(context.Background(), &params)

	videoURL := fmt.Sprintf("%s%s", cfg.s3CfDistribution, imageFile)

	newVideo := database.Video{
		ID:                videoID,
		ThumbnailURL:      videoMetadata.ThumbnailURL,
		CreateVideoParams: videoMetadata.CreateVideoParams,
		VideoURL:          &videoURL,
	}

	err = cfg.db.UpdateVideo(newVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Database error", err)
		return
	}

	respondWithJSON(w, http.StatusOK, newVideo)

}

func getVideoAspectRatio(filePath string) (string, error) {
	type videoData struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		}
	}

	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	var b bytes.Buffer
	cmd.Stdout = &b

	err := cmd.Run()
	if err != nil {
		return "", err
	}

	var v videoData
	err = json.Unmarshal(b.Bytes(), &v)
	if err != nil {
		return "", err
	}

	if v.Streams[0].Width/16 == v.Streams[0].Height/9 {
		return "landscape", nil
	}
	if v.Streams[0].Width/9 == v.Streams[0].Height/16 {
		return "portrait", nil
	}
	return "other", nil
}

func processVideoForFastStart(filePath string) (string, error) {
	outputPath := fmt.Sprintf("%s.processing", filePath)

	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputPath)
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	return outputPath, nil
}
