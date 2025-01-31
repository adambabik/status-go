package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/status-im/status-go/protocol/identity/identicon"
	"github.com/status-im/status-go/protocol/images"
)

var globalCertificate *tls.Certificate = nil
var globalPem string

func generateTLSCert() error {
	if globalCertificate != nil {
		return nil
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour)

	cert, err := GenerateX509Cert(notBefore, notAfter)
	if err != nil {
		return err
	}

	certPem, keyPem, err := GenerateX509PEMs(cert, priv)
	if err != nil {
		return err
	}

	finalCert, err := tls.X509KeyPair(certPem, keyPem)
	if err != nil {
		return err
	}

	globalCertificate = &finalCert
	globalPem = string(certPem)
	return nil
}

func PublicTLSCert() (string, error) {
	err := generateTLSCert()

	if err != nil {
		return "", err
	}

	return globalPem, nil
}

type imageHandler struct {
	db     *sql.DB
	logger *zap.Logger
}

type audioHandler struct {
	db     *sql.DB
	logger *zap.Logger
}

type identiconHandler struct {
	logger *zap.Logger
}

func (s *identiconHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	pks, ok := r.URL.Query()["publicKey"]
	if !ok || len(pks) == 0 {
		s.logger.Error("no publicKey")
		return
	}
	pk := pks[0]
	image, err := identicon.Generate(pk)
	if err != nil {
		s.logger.Error("could not generate identicon")
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age:290304000, public")
	w.Header().Set("Expires", time.Now().AddDate(60, 0, 0).Format(http.TimeFormat))

	_, err = w.Write(image)
	if err != nil {
		s.logger.Error("failed to write image", zap.Error(err))
	}
}

func (s *imageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	messageIDs, ok := r.URL.Query()["messageId"]
	if !ok || len(messageIDs) == 0 {
		s.logger.Error("no messageID")
		return
	}
	messageID := messageIDs[0]
	var image []byte
	err := s.db.QueryRow(`SELECT image_payload FROM user_messages WHERE id = ?`, messageID).Scan(&image)
	if err != nil {
		s.logger.Error("failed to find image", zap.Error(err))
		return
	}
	if len(image) == 0 {
		s.logger.Error("empty image")
		return
	}
	mime, err := images.ImageMime(image)
	if err != nil {
		s.logger.Error("failed to get mime", zap.Error(err))
	}

	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-store")

	_, err = w.Write(image)
	if err != nil {
		s.logger.Error("failed to write image", zap.Error(err))
	}
}

func (s *audioHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	messageIDs, ok := r.URL.Query()["messageId"]
	if !ok || len(messageIDs) == 0 {
		s.logger.Error("no messageID")
		return
	}
	messageID := messageIDs[0]
	var audio []byte
	err := s.db.QueryRow(`SELECT audio_payload FROM user_messages WHERE id = ?`, messageID).Scan(&audio)
	if err != nil {
		s.logger.Error("failed to find image", zap.Error(err))
		return
	}
	if len(audio) == 0 {
		s.logger.Error("empty audio")
		return
	}

	w.Header().Set("Content-Type", "audio/aac")
	w.Header().Set("Cache-Control", "no-store")

	_, err = w.Write(audio)
	if err != nil {
		s.logger.Error("failed to write audio", zap.Error(err))
	}
}

type Server struct {
	Port   int
	run    bool
	server *http.Server
	logger *zap.Logger
	db     *sql.DB
	cert   *tls.Certificate
}

func NewServer(db *sql.DB, logger *zap.Logger) (*Server, error) {
	err := generateTLSCert()

	if err != nil {
		return nil, err
	}

	return &Server{db: db, logger: logger, cert: globalCertificate, Port: 0}, nil
}

func (s *Server) listenAndServe() {
	cfg := &tls.Config{Certificates: []tls.Certificate{*s.cert}, ServerName: "localhost", MinVersion: tls.VersionTLS12}

	// in case of restart, we should use the same port as the first start in order not to break existing links
	addr := fmt.Sprintf("localhost:%d", s.Port)

	listener, err := tls.Listen("tcp", addr, cfg)
	if err != nil {
		s.logger.Error("failed to start server, retrying", zap.Error(err))
		s.Port = 0
		err = s.Start()
		if err != nil {
			s.logger.Error("server start failed, giving up", zap.Error(err))
		}
		return
	}

	s.Port = listener.Addr().(*net.TCPAddr).Port
	s.run = true
	err = s.server.Serve(listener)
	if err != http.ErrServerClosed {
		s.logger.Error("server failed unexpectedly, restarting", zap.Error(err))
		err = s.Start()
		if err != nil {
			s.logger.Error("server start failed, giving up", zap.Error(err))
		}
		return
	}

	s.run = false
}

func (s *Server) Start() error {
	handler := http.NewServeMux()
	handler.Handle("/messages/images", &imageHandler{db: s.db, logger: s.logger})
	handler.Handle("/messages/audio", &audioHandler{db: s.db, logger: s.logger})
	handler.Handle("/messages/identicons", &identiconHandler{logger: s.logger})
	s.server = &http.Server{Handler: handler}

	go s.listenAndServe()

	return nil
}

func (s *Server) Stop() error {
	if s.server != nil {
		return s.server.Shutdown(context.Background())
	}

	return nil
}

func (s *Server) ToForeground() {
	if !s.run && (s.server != nil) {
		err := s.Start()
		if err != nil {
			s.logger.Error("server start failed during foreground transition", zap.Error(err))
		}
	}
}

func (s *Server) ToBackground() {
	if s.run {
		err := s.Stop()
		if err != nil {
			s.logger.Error("server stop failed during background transition", zap.Error(err))
		}
	}
}
