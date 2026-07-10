// Package api provides the Tater Tube Server REST API.
//
//	@title			Tater Tube Server API
//	@version		1.0
//	@description	REST API for Tater Tube Server — Usenet/NZB stream preparation, queue management, provider setup, and Stremio-compatible playback.
//
//	@contact.name	Tater Tube
//
//	@license.name	MIT
//
//	@host		localhost:8080
//	@BasePath	/api
//
//	@securityDefinitions.apikey	ApiKeyAuth
//	@in							query
//	@name						apikey
//
//	@securityDefinitions.http	BearerAuth
//	@scheme						bearer
//	@bearerFormat				JWT
//
//	@tag.name			Queue
//	@tag.description	NZB download queue management
//	@tag.name			Health
//	@tag.description	File health monitoring and repair
//	@tag.name			Files
//	@tag.description	File metadata, streams, and NZB export
//	@tag.name			Import
//	@tag.description	Manual file imports and directory scanning
//	@tag.name			Providers
//	@tag.description	NNTP provider management
//	@tag.name			ARRs
//	@tag.description	Sonarr/Radarr integration
//	@tag.name			Config
//	@tag.description	Configuration management
//	@tag.name			System
//	@tag.description	System stats, health, and maintenance
//	@tag.name			Stremio
//	@tag.description	Stremio addon and NZB stream endpoints
//	@tag.name			Auth
//	@tag.description	Authentication and registration
//	@tag.name			User
//	@tag.description	Current user management
package api
