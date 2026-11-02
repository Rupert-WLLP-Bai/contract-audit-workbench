// File metadata (manifest mirror):
// Purpose: Bootstraps the administration application by mounting the admin route tree and shared providers into the admin HTML root.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-25
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { AdminApp } from "./admin-app";
import { mountApplication } from "./bootstrap";

mountApplication(<AdminApp />);
