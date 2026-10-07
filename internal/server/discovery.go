// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/gin-gonic/gin"
	"github.com/nomifun/nomifun-model-gateway/internal/relay"
)

func (s *Server) discoverChannel(c *gin.Context) {
	id, ok := s.id(c)
	if !ok {
		return
	}
	result, err := s.Relay.DiscoverChannel(c.Request.Context(), id)
	if err != nil {
		s.fail(c, err)
		return
	}
	if !s.audit(c, "channel.discover", strconvID(id)) {
		return
	}
	c.JSON(200, result)
}

func (s *Server) discoverChannelPreview(c *gin.Context) {
	var in relay.DiscoveryInput
	if !s.decode(c, &in) {
		return
	}
	result, err := s.Relay.DiscoverPreview(c.Request.Context(), in)
	if err != nil {
		s.fail(c, err)
		return
	}
	// Only protocol kind is recorded; URLs, credentials and upstream bodies
	// never become audit resources for an unsaved preview.
	if !s.audit(c, "channel.discover_preview", in.Kind) {
		return
	}
	c.JSON(200, result)
}
