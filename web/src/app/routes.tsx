import type { RouteDefinition } from "@solidjs/router";
import { HomePage } from "~/features/home/home-page";
import { ProjectPage } from "~/features/project/project-page";
import { ServicePage } from "~/features/service/service-page";
import { TaskPage } from "~/features/task/task-page";
import { GitPage } from "~/features/git/git-page";
import { SettingsPage } from "~/features/settings/settings-page";
import { NotFoundPage } from "./not-found";

export const routes: RouteDefinition[] = [
  { path: "/", component: HomePage },
  { path: "/projects/:project", component: ProjectPage },
  { path: "/projects/:project/services/:service", component: ServicePage },
  { path: "/projects/:project/tasks/:task", component: TaskPage },
  { path: "/projects/:project/git", component: GitPage },
  { path: "/settings", component: SettingsPage },
  { path: "*404", component: NotFoundPage },
];
