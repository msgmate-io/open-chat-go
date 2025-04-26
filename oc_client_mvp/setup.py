from setuptools import setup, find_packages
# pip install git+https://github.com/username/repo.git#subdirectory=client

setup(
    name="oc_python_client",
    version="0.1",
    packages=find_packages(),
    python_requires=">=3.8, <4",
    install_requires=[
        "pycryptodome",
        "inquirer",
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