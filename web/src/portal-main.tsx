// File metadata (manifest mirror):
// Purpose: Mounts the API documentation page into the portal HTML root with routing disabled; the portal intentionally renders this one public reference page.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-25
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import ApiDocsPage from "./pages/api-docs-page";
import { mountApplication } from "./bootstrap";

mountApplication(<ApiDocsPage />, { routing: false });
