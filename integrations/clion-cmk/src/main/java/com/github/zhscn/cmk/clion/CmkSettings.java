package com.github.zhscn.cmk.clion;

import com.intellij.openapi.components.PersistentStateComponent;
import com.intellij.openapi.components.Service;
import com.intellij.openapi.components.State;
import com.intellij.openapi.components.Storage;
import com.intellij.openapi.project.Project;
import org.jetbrains.annotations.NotNull;

@Service(Service.Level.PROJECT)
@State(name = "CmkSettings", storages = @Storage("cmk.xml"))
public final class CmkSettings implements PersistentStateComponent<CmkSettings.Data> {
  public static final class Data {
    public boolean enabled = true;
    public String executable = "cmk";
  }

  private Data data = new Data();

  public static CmkSettings getInstance(Project project) {
    return project.getService(CmkSettings.class);
  }

  public boolean isEnabled() {
    return data.enabled;
  }

  public void setEnabled(boolean enabled) {
    data.enabled = enabled;
  }

  public String getExecutable() {
    return data.executable == null || data.executable.isBlank() ? "cmk" : data.executable;
  }

  public void setExecutable(String executable) {
    data.executable = executable == null ? "cmk" : executable.strip();
  }

  @Override
  public @NotNull Data getState() {
    return data;
  }

  @Override
  public void loadState(@NotNull Data state) {
    data = state;
  }
}
