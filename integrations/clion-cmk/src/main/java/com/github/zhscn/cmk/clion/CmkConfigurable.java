package com.github.zhscn.cmk.clion;

import com.intellij.execution.ExecutionException;
import com.intellij.execution.configurations.GeneralCommandLine;
import com.intellij.execution.process.CapturingProcessHandler;
import com.intellij.execution.process.ProcessOutput;
import com.intellij.openapi.options.Configurable;
import com.intellij.openapi.project.Project;
import java.awt.BorderLayout;
import java.awt.FlowLayout;
import java.awt.GridBagConstraints;
import java.awt.GridBagLayout;
import java.awt.Insets;
import javax.swing.JButton;
import javax.swing.JCheckBox;
import javax.swing.JComponent;
import javax.swing.JLabel;
import javax.swing.JPanel;
import javax.swing.JTextField;
import org.jetbrains.annotations.Nls;
import org.jetbrains.annotations.Nullable;

public final class CmkConfigurable implements Configurable {
  private final Project project;
  private JPanel panel;
  private JCheckBox enabled;
  private JTextField executable;
  private JLabel status;

  public CmkConfigurable(Project project) {
    this.project = project;
  }

  @Override
  public @Nls String getDisplayName() {
    return "cmk";
  }

  @Override
  public @Nullable JComponent createComponent() {
    enabled = new JCheckBox("Enable cmk configure integration");
    executable = new JTextField();
    status = new JLabel(" ");
    JButton test = new JButton("Test");
    test.addActionListener(event -> testExecutable());

    JPanel executableRow = new JPanel(new BorderLayout(8, 0));
    executableRow.add(executable, BorderLayout.CENTER);
    executableRow.add(test, BorderLayout.EAST);

    panel = new JPanel(new GridBagLayout());
    GridBagConstraints constraints = new GridBagConstraints();
    constraints.gridx = 0;
    constraints.gridy = 0;
    constraints.gridwidth = 2;
    constraints.weightx = 1;
    constraints.fill = GridBagConstraints.HORIZONTAL;
    constraints.anchor = GridBagConstraints.WEST;
    constraints.insets = new Insets(0, 0, 12, 0);
    panel.add(enabled, constraints);

    constraints.gridy++;
    constraints.gridwidth = 1;
    constraints.weightx = 0;
    constraints.insets = new Insets(0, 0, 8, 8);
    panel.add(new JLabel("cmk executable:"), constraints);

    constraints.gridx = 1;
    constraints.weightx = 1;
    constraints.insets = new Insets(0, 0, 8, 0);
    panel.add(executableRow, constraints);

    constraints.gridx = 0;
    constraints.gridy++;
    constraints.gridwidth = 2;
    constraints.weighty = 1;
    constraints.anchor = GridBagConstraints.NORTHWEST;
    constraints.insets = new Insets(0, 0, 0, 0);
    panel.add(status, constraints);

    reset();
    return panel;
  }

  @Override
  public boolean isModified() {
    CmkSettings settings = CmkSettings.getInstance(project);
    return enabled.isSelected() != settings.isEnabled()
        || !executable.getText().strip().equals(settings.getExecutable());
  }

  @Override
  public void apply() {
    CmkSettings settings = CmkSettings.getInstance(project);
    settings.setEnabled(enabled.isSelected());
    settings.setExecutable(executable.getText());
  }

  @Override
  public void reset() {
    CmkSettings settings = CmkSettings.getInstance(project);
    enabled.setSelected(settings.isEnabled());
    executable.setText(settings.getExecutable());
    status.setText(" ");
  }

  @Override
  public void disposeUIResources() {
    panel = null;
    enabled = null;
    executable = null;
    status = null;
  }

  private void testExecutable() {
    String command = executable.getText().strip();
    if (command.isEmpty()) {
      command = "cmk";
    }
    try {
      ProcessOutput output =
          new CapturingProcessHandler(new GeneralCommandLine(command, "--version"))
              .runProcess(5000);
      String version = output.getStdout().strip();
      if (output.getExitCode() == 0 && !version.isEmpty()) {
        status.setText("Available: " + version);
      } else {
        String detail = output.getStderr().strip();
        status.setText(detail.isEmpty() ? "cmk exited with an error" : detail);
      }
    } catch (ExecutionException e) {
      status.setText("Unavailable: " + e.getMessage());
    }
  }
}
