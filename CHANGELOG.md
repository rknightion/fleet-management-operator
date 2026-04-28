# Changelog

All notable changes to the Fleet Management Operator will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial release of Fleet Management Operator
- Pipeline CRD for managing Fleet Management pipelines
- Support for Alloy and OpenTelemetry Collector configurations
- Multi-architecture Docker images (linux/amd64, linux/arm64)
- Helm chart for easy deployment
- Source tracking (Git, Terraform, Kubernetes)
- Finalizer support for proper cleanup
- Status conditions following Kubernetes conventions
- Metrics endpoint on port 8080
- Leader election for high availability

### Changed

### Deprecated

### Removed

### Fixed

### Security

### Upgrade Notes
<!-- Per-release: list CRD schema changes, Helm value renames, drainage requirements, and manual steps required. -->

## [0.1.0] - YYYY-MM-DD

### Added
- Initial release

[Unreleased]: https://github.com/grafana/fleet-management-operator/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/grafana/fleet-management-operator/releases/tag/v0.1.0
