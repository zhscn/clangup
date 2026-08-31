package com.github.zhscn.cmk.clion;

import com.intellij.execution.ExecutionException;
import com.intellij.execution.configurations.GeneralCommandLine;
import com.intellij.execution.process.CapturingProcessHandler;
import com.intellij.execution.process.ProcessOutput;
import com.intellij.openapi.diagnostic.Logger;
import com.intellij.openapi.project.Project;
import com.intellij.openapi.util.Key;
import com.jetbrains.cidr.cpp.cmake.CMakeException;
import com.jetbrains.cidr.cpp.cmake.CMakeRunnerStep;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import org.jetbrains.annotations.NotNull;

/** CLion-version adapter for the internal CMake runner-step API. */
public final class CmkRunnerStep implements CMakeRunnerStep {
  private static final Logger LOG = Logger.getInstance(CmkRunnerStep.class);
  private static final Key<Boolean> CMK_CONFIGURE = Key.create("cmk.configure");

  @Override
  public @NotNull Parameters modifyParameters(
      @NotNull Project project, @NotNull Parameters parameters) {
    if (!CmkSettings.getInstance(project).isEnabled()
        || !GeneratedPreset.isSelected(parameters.getParameters())
        || !Files.isRegularFile(parameters.getProjectDir().resolve("cmk.yaml"))) {
      return parameters;
    }

    Parameters modified = parameters.withParameters(List.of("-E", "true"));
    modified.putUserData(CMK_CONFIGURE, true);
    return modified;
  }

  @Override
  public void beforeGeneration(@NotNull Project project, @NotNull Parameters parameters)
      throws CMakeException {
    if (!Boolean.TRUE.equals(parameters.getUserData(CMK_CONFIGURE))) {
      return;
    }

    Path projectDir = parameters.getProjectDir();
    String executable = CmkSettings.getInstance(project).getExecutable();
    GeneralCommandLine commandLine =
        new GeneralCommandLine(
                executable,
                "ensure-configured",
                "--build",
                parameters.getOutputDir().toString())
            .withWorkDirectory(projectDir.toFile());
    try {
      LOG.info("Running " + commandLine.getCommandLineString());
      CapturingProcessHandler handler = new CapturingProcessHandler(commandLine);
      parameters.getListener().processStarted(handler);
      ProcessOutput output = handler.runProcess();
      if (output.getExitCode() != 0) {
        throw new CMakeException(
            "cmk configuration failed with exit code "
                + output.getExitCode()
                + formatOutput(output));
      }
      String detail = formatOutput(output);
      LOG.info("cmk configuration is ready" + detail);
    } catch (ExecutionException e) {
      throw new CMakeException(
          "cannot start cmk executable '" + executable + "': " + e.getMessage(), e);
    }
  }

  static @NotNull String formatOutput(@NotNull ProcessOutput output) {
    String stdout = output.getStdout().strip();
    String stderr = output.getStderr().strip();
    StringBuilder detail = new StringBuilder();
    if (!stdout.isEmpty()) {
      detail.append("\nstdout:\n").append(stdout);
    }
    if (!stderr.isEmpty()) {
      detail.append("\nstderr:\n").append(stderr);
    }
    return detail.toString();
  }
}
