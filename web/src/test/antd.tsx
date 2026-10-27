import { ConfigProvider } from "antd";
import { StyleProvider } from "@ant-design/cssinjs";
import zhCN from "antd/locale/zh_CN";
import { render, screen, type RenderOptions } from "@testing-library/react";
import type { UserEvent } from "@testing-library/user-event";
import type { ReactElement } from "react";

export function renderWithAntd(ui: ReactElement, options?: RenderOptions) {
  return render(
    <StyleProvider layer>
      <ConfigProvider
        virtual={false}
        locale={zhCN}
        button={{ autoInsertSpace: false }}
      >
        {ui}
      </ConfigProvider>
    </StyleProvider>,
    options,
  );
}

export async function chooseOption(
  user: UserEvent,
  control: HTMLElement,
  name: string | RegExp,
) {
  await user.click(control);
  await user.click(await screen.findByRole("option", { name }));
}
