from setuptools import setup, find_packages
# pip install git+https://github.com/msgmate-io/open-chat-go#subdirectory=oc_client_mvp

setup(
    name="oc_client_mvp",
    version="0.1",
    packages=find_packages(),
    python_requires=">=3.8, <4",
    install_requires=[
        "requests",
    ],
    entry_points={
        "console_scripts": [
            "oc_client=oc_client_mvp.client:main",
        ],
    },
    classifiers=[
        "Programming Language :: Python :: 3",
    ],
)