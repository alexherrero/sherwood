package api

import (
	"net/http"
	"strconv"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/go-chi/chi/v5"
)

// GetNotificationsHandler retrieves recent notifications.
//
// @Summary      Get Notifications
// @Description  Retrieves a list of recent system notifications.
// @Tags         notifications
// @Accept       json
// @Produce      json
// @Param        limit   query     int  false  "Limit (default 50)"
// @Param        offset  query     int  false  "Offset (default 0)"
// @Success      200  {array}   models.Notification
// @Failure      500  {object}  ErrorResponse
// @Router       /notifications [get]
func (h *Handler) GetNotificationsHandler(w http.ResponseWriter, r *http.Request) {
	if h.notificationManager == nil {
		writeError(w, http.StatusNotImplemented, "Notification manager not initialized")
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	notifs, err := h.notificationManager.GetHistory(limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to retrieve notifications")
		return
	}

	if notifs == nil {
		notifs = []models.Notification{}
	}

	writeJSON(w, http.StatusOK, notifs)
}

// MarkNotificationReadHandler marks a single notification as read.
//
// @Summary      Mark Notification Read
// @Description  Marks a specific notification as read.
// @Tags         notifications
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "Notification ID"
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /notifications/{id}/read [put]
func (h *Handler) MarkNotificationReadHandler(w http.ResponseWriter, r *http.Request) {
	if h.notificationManager == nil {
		writeError(w, http.StatusNotImplemented, "Notification manager not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Missing notification ID")
		return
	}

	if err := h.notificationManager.MarkAsRead(id); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to mark notification as read")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// MarkAllReadHandler marks all notifications as read.
//
// @Summary      Mark All Notifications Read
// @Description  Marks all system notifications as read.
// @Tags         notifications
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]string
// @Failure      500  {object}  ErrorResponse
// @Router       /notifications/read-all [put]
func (h *Handler) MarkAllReadHandler(w http.ResponseWriter, r *http.Request) {
	if h.notificationManager == nil {
		writeError(w, http.StatusNotImplemented, "Notification manager not initialized")
		return
	}

	if err := h.notificationManager.MarkAllAsRead(); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to mark all notifications as read")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "success"})
}
